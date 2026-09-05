package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	runtimeevents "github.com/hollis-labs/go-runtime-events/runtimeevents"
	"github.com/hollis-labs/nanite/internal/store"
)

const (
	hostRuntimeFeedQueueCapacity     = 1024
	hostRuntimeFeedRetention         = 512
	hostRuntimePayloadMaxBytes       = 4096
	hostRuntimeEventMaxBytes         = store.HostRuntimeFeedMaxEventBytes
	hostRuntimeStringMaxBytes        = 384
	hostRuntimeLossLedgerMaxSessions = 256
)

var (
	hostRuntimeSecretValue            = regexp.MustCompile(`(?i)(bearer\s+[^\s,;]+|sk-[a-z0-9_-]{8,}|gh[pousr]_[a-z0-9_]{8,}|(?:api[_-]?key|token|secret|password|authorization|cookie|credential)\s*[:=]\s*[^\s,;]+)`)
	hostRuntimeSecretKey              = regexp.MustCompile(`(?i)^(api.?key|token|secret|password|authorization|cookie|credential|private.?key|raw.?input|arguments?|args|prompt|stdin|stdout|stderr|result|output|content|bytes|terminal.?output)$`)
	errHostRuntimeLossLedgerSaturated = errors.New("host runtime loss ledger is saturated")
)

// HostRuntimeFeed owns the bounded ingestion FIFO between wrapper IO and the
// durable public feed. Wrapper sinks never perform SQLite or network IO.
// A single FIFO also gives overlapping old/new wrapper runs for one session a
// deterministic host order; runtime_run_id lets reducers ignore stale exits.
type HostRuntimeFeed struct {
	store *store.Store

	lifecycleMu    sync.RWMutex
	mu             sync.Mutex
	closed         bool
	queue          chan store.HostRuntimeEvent
	pending        map[string]*hostRuntimeDrop
	asyncErr       error
	workerCtx      context.Context
	cancelWorker   context.CancelFunc
	reserveCtx     context.Context
	cancelReserves context.CancelFunc
	done           chan struct{}

	persistFn func(context.Context, store.HostRuntimeEvent) (bool, error)
	reserveFn func(context.Context, string, string) (int64, error)
}

type hostRuntimeDrop struct {
	count              int64
	firstSequence      uint64
	lastSequence       uint64
	firstRunID         string
	lastRunID          string
	maxGeneration      int64
	maxGenerationRunID string
	spansRuns          bool
	countTruncated     bool
}

func NewHostRuntimeFeed(s *store.Store) *HostRuntimeFeed {
	workerCtx, cancelWorker := context.WithCancel(context.Background())
	reserveCtx, cancelReserves := context.WithCancel(context.Background())
	f := &HostRuntimeFeed{
		store:          s,
		queue:          make(chan store.HostRuntimeEvent, hostRuntimeFeedQueueCapacity),
		pending:        make(map[string]*hostRuntimeDrop),
		workerCtx:      workerCtx,
		cancelWorker:   cancelWorker,
		reserveCtx:     reserveCtx,
		cancelReserves: cancelReserves,
		done:           make(chan struct{}),
	}
	if s != nil {
		f.persistFn = func(ctx context.Context, event store.HostRuntimeEvent) (bool, error) {
			_, inserted, err := s.AppendHostRuntimeEvent(ctx, event, hostRuntimeFeedRetention)
			return inserted, err
		}
		f.reserveFn = s.ReserveHostRuntimeRun
	}
	go f.run()
	return f
}

func (f *HostRuntimeFeed) ReserveRuntimeGeneration(ctx context.Context, sessionID, runID string) (int64, error) {
	if f == nil || f.reserveFn == nil {
		return 0, errors.New("host runtime feed is unavailable")
	}
	f.lifecycleMu.RLock()
	defer f.lifecycleMu.RUnlock()
	f.mu.Lock()
	closed := f.closed
	f.mu.Unlock()
	if closed {
		return 0, errors.New("host runtime feed is closed")
	}
	reserveCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(f.reserveCtx, cancel)
	defer func() {
		stop()
		cancel()
	}()
	return f.reserveFn(reserveCtx, sessionID, runID)
}

// Publish projects and copies ev before returning, then admits it without
// blocking the wrapper runtime. Queue overflow is accumulated per session and
// becomes a durable host_runtime.ingest_gap record before the next event for
// that session, including when the next event belongs to a successor run.
func (f *HostRuntimeFeed) Publish(_ context.Context, runtimeRunID string, runtimeGeneration int64, isACP bool, ev runtimeevents.Event) error {
	if f == nil || f.store == nil {
		return nil
	}
	if runtimeRunID == "" || runtimeGeneration < 1 {
		return errors.New("host runtime feed run identity is incomplete")
	}
	projected := projectHostRuntimeEvent(runtimeRunID, runtimeGeneration, isACP, ev)
	sessionID := projected.SessionID

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return errors.New("host runtime feed is closed")
	}
	if f.asyncErr != nil {
		return f.asyncErr
	}
	if pending := f.pending[sessionID]; pending != nil {
		select {
		case f.queue <- newHostRuntimeIngestGap(sessionID, *pending):
			delete(f.pending, sessionID)
		default:
			if err := f.noteDropLocked(projected); err != nil {
				return err
			}
			return f.asyncErr
		}
	}
	select {
	case f.queue <- projected:
	default:
		if err := f.noteDropLocked(projected); err != nil {
			return err
		}
	}
	return f.asyncErr
}

func (f *HostRuntimeFeed) noteDropLocked(event store.HostRuntimeEvent) error {
	if err := addHostRuntimeDrop(f.pending, event); err != nil {
		f.asyncErr = err
		slog.Error("host runtime feed: publish loss ledger saturated", "sessions", len(f.pending), "session_id", event.SessionID)
		f.cancelWorker()
		return err
	}
	return nil
}

func addHostRuntimeDrop(ledger map[string]*hostRuntimeDrop, event store.HostRuntimeEvent) error {
	drop := ledger[event.SessionID]
	if drop == nil {
		if len(ledger) >= hostRuntimeLossLedgerMaxSessions {
			return errHostRuntimeLossLedgerSaturated
		}
		drop = &hostRuntimeDrop{}
		ledger[event.SessionID] = drop
	}
	mergeHostRuntimeDrop(drop, dropFromHostRuntimeEvent(event))
	return nil
}

func newHostRuntimeIngestGap(sessionID string, drop hostRuntimeDrop) store.HostRuntimeEvent {
	payload, _ := json.Marshal(map[string]any{
		"dropped_events":        drop.count,
		"first_source_sequence": drop.firstSequence,
		"last_source_sequence":  drop.lastSequence,
		"spans_runtime_runs":    drop.spansRuns,
		"count_truncated":       drop.countTruncated,
	})
	event := store.HostRuntimeEvent{
		SchemaVersion:     store.HostRuntimeFeedSchemaVersion,
		SessionID:         sessionID,
		RuntimeRunID:      drop.maxGenerationRunID,
		RuntimeGeneration: drop.maxGeneration,
		SourceEventID:     "nanite-gap-" + uuid.NewString(),
		Kind:              "host_runtime.ingest_gap",
		OccurredAt:        time.Now().UTC().Format(time.RFC3339Nano),
		Source:            store.HostRuntimeEventSource{Channel: "nanite-host", Confidence: "exact"},
		Payload:           payload,
		PayloadVisibility: "host_control",
	}
	return event
}

func (f *HostRuntimeFeed) run() {
	defer close(f.done)
	failed := make(map[string]*hostRuntimeDrop)
	for event := range f.queue {
		if f.workerCtx.Err() != nil {
			break
		}
		sessionID := event.SessionID
		if pending := failed[sessionID]; pending != nil {
			if err := f.persist(newHostRuntimeIngestGap(sessionID, *pending)); err != nil {
				if f.workerCtx.Err() != nil {
					break
				}
				mergeHostRuntimeDrop(pending, dropFromHostRuntimeEvent(event))
				slog.Warn("host runtime feed: persist recovery gap", "session_id", event.SessionID, "runtime_run_id", event.RuntimeRunID, "err", err)
				continue
			}
			delete(failed, sessionID)
		}
		if err := f.persist(event); err != nil {
			if f.workerCtx.Err() != nil {
				break
			}
			if err := addHostRuntimeDrop(failed, event); err != nil {
				f.setAsyncError(err)
				slog.Error("host runtime feed: persistence loss ledger saturated", "sessions", len(failed), "session_id", sessionID)
				continue
			}
			slog.Warn("host runtime feed: persist event", "session_id", event.SessionID, "runtime_run_id", event.RuntimeRunID, "kind", event.Kind, "err", err)
		}
	}
	if f.workerCtx.Err() != nil {
		if len(failed) > 0 || len(f.queue) > 0 {
			slog.Warn("host runtime feed: canceled with unpersisted records", "failed_sessions", len(failed), "queued_events", len(f.queue))
		}
		return
	}
	for sessionID, pending := range failed {
		if err := f.persist(newHostRuntimeIngestGap(sessionID, *pending)); err != nil {
			slog.Warn("host runtime feed: final persistence gap unavailable", "session_id", sessionID, "runtime_run_id", pending.lastRunID, "dropped_events", pending.count, "err", err)
		}
	}
}

func (f *HostRuntimeFeed) persist(event store.HostRuntimeEvent) error {
	ctx, cancel := context.WithTimeout(f.workerCtx, 3*time.Second)
	inserted, err := f.persistFn(ctx, event)
	cancel()
	if err != nil {
		return err
	}
	if !inserted {
		slog.Debug("host runtime feed: duplicate source event ignored", "session_id", event.SessionID, "runtime_run_id", event.RuntimeRunID, "source_event_id", event.SourceEventID)
	}
	return nil
}

func dropFromHostRuntimeEvent(event store.HostRuntimeEvent) hostRuntimeDrop {
	if event.Kind != "host_runtime.ingest_gap" {
		return hostRuntimeDrop{
			count: 1, firstSequence: event.SourceSequence, lastSequence: event.SourceSequence,
			firstRunID: event.RuntimeRunID, lastRunID: event.RuntimeRunID,
			maxGeneration: event.RuntimeGeneration, maxGenerationRunID: event.RuntimeRunID,
		}
	}
	var payload struct {
		Count     int64  `json:"dropped_events"`
		First     uint64 `json:"first_source_sequence"`
		Last      uint64 `json:"last_source_sequence"`
		Spans     bool   `json:"spans_runtime_runs"`
		Truncated bool   `json:"count_truncated"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil || payload.Count < 1 {
		return hostRuntimeDrop{count: 1, firstRunID: event.RuntimeRunID, lastRunID: event.RuntimeRunID, maxGeneration: event.RuntimeGeneration, maxGenerationRunID: event.RuntimeRunID}
	}
	return hostRuntimeDrop{
		count: payload.Count, firstSequence: payload.First, lastSequence: payload.Last,
		firstRunID: event.RuntimeRunID, lastRunID: event.RuntimeRunID,
		maxGeneration: event.RuntimeGeneration, maxGenerationRunID: event.RuntimeRunID, spansRuns: payload.Spans, countTruncated: payload.Truncated,
	}
}

func mergeHostRuntimeDrop(dst *hostRuntimeDrop, incoming hostRuntimeDrop) {
	if incoming.count < 1 {
		return
	}
	if dst.count == 0 {
		dst.firstSequence = incoming.firstSequence
		dst.firstRunID = incoming.firstRunID
	}
	if incoming.count > math.MaxInt64-dst.count {
		dst.count = math.MaxInt64
		dst.countTruncated = true
	} else {
		dst.count += incoming.count
	}
	dst.countTruncated = dst.countTruncated || incoming.countTruncated
	dst.lastSequence = incoming.lastSequence
	if dst.firstRunID != incoming.lastRunID || incoming.spansRuns {
		dst.spansRuns = true
	}
	dst.lastRunID = incoming.lastRunID
	if incoming.maxGeneration > dst.maxGeneration {
		dst.maxGeneration = incoming.maxGeneration
		dst.maxGenerationRunID = incoming.maxGenerationRunID
	}
}

func (f *HostRuntimeFeed) setAsyncError(err error) {
	f.mu.Lock()
	if f.asyncErr == nil {
		f.asyncErr = err
	}
	f.mu.Unlock()
	f.cancelWorker()
}

// Close stops admission, flushes any queued overflow markers, and drains all
// admitted work before returning or ctx expires. It is called after Chat has
// stopped wrapper producers and before the process closes the Store.
func (f *HostRuntimeFeed) Close(ctx context.Context) error {
	if f == nil {
		return nil
	}
	f.cancelReserves()
	f.lifecycleMu.Lock()
	f.mu.Lock()
	var flushErr error
	if !f.closed {
		f.closed = true
	flushPending:
		for sessionID, drop := range f.pending {
			select {
			case f.queue <- newHostRuntimeIngestGap(sessionID, *drop):
				delete(f.pending, sessionID)
			case <-ctx.Done():
				flushErr = ctx.Err()
				break flushPending
			}
		}
		if len(f.pending) > 0 {
			slog.Warn("host runtime feed: shutdown before all overflow gaps were admitted", "runtime_runs", len(f.pending))
		}
		close(f.queue)
	}
	f.mu.Unlock()
	f.lifecycleMu.Unlock()
	if flushErr != nil {
		f.mu.Lock()
		pendingSessions := len(f.pending)
		f.mu.Unlock()
		slog.Warn("host runtime feed: close could not enqueue all loss records", "pending_sessions", pendingSessions, "err", flushErr)
	}
	select {
	case <-f.done:
		f.cancelWorker()
		f.mu.Lock()
		asyncErr := f.asyncErr
		f.mu.Unlock()
		if flushErr != nil {
			return flushErr
		}
		return asyncErr
	case <-ctx.Done():
		f.cancelWorker()
		<-f.done
		return ctx.Err()
	}
}

func projectHostRuntimeEvent(runtimeRunID string, runtimeGeneration int64, isACP bool, ev runtimeevents.Event) store.HostRuntimeEvent {
	payload, visibility, truncated := projectHostRuntimePayload(ev.Kind, isACP, ev.Payload)
	occurredAt := ev.Time.UTC().Format(time.RFC3339Nano)
	if ev.Time.IsZero() {
		occurredAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	event := store.HostRuntimeEvent{
		SchemaVersion:     store.HostRuntimeFeedSchemaVersion,
		SessionID:         boundedRuntimeString(ev.SessionID, 256),
		RuntimeRunID:      boundedRuntimeString(runtimeRunID, 128),
		RuntimeGeneration: runtimeGeneration,
		SourceEventID:     boundedRuntimeString(ev.ID, 256),
		SourceSequence:    ev.Sequence,
		Kind:              boundedRuntimeString(string(ev.Kind), 128),
		OccurredAt:        occurredAt,
		TurnID:            boundedRuntimeString(ev.TurnID, 256),
		ParentID:          boundedRuntimeString(ev.ParentID, 256),
		Source: store.HostRuntimeEventSource{
			Channel:    boundedRuntimeString(string(ev.Source.Channel), 96),
			Confidence: boundedRuntimeString(string(ev.Source.Confidence), 32),
		},
		Process: store.HostRuntimeProcess{
			Provider:          boundedRuntimeString(ev.Process.Provider, 96),
			Runtime:           boundedRuntimeString(ev.Process.Runtime, 96),
			ProviderSessionID: redactRuntimeString(boundedRuntimeString(ev.Process.ProviderSessionID, 256)),
		},
		Payload:           payload,
		PayloadTruncated:  truncated,
		PayloadVisibility: visibility,
	}
	encoded, err := json.Marshal(event)
	if err == nil && len(encoded) <= hostRuntimeEventMaxBytes {
		return event
	}
	// Metadata identifiers are bounded above, but JSON escaping can expand
	// hostile control-heavy strings. Keep routing identity and lifecycle while
	// removing optional correlation/provider detail at the final hard bound.
	event.TurnID = ""
	event.ParentID = ""
	event.Process.ProviderSessionID = ""
	event.Payload, _ = json.Marshal(map[string]any{
		"payload_omitted": true,
		"size_limit":      hostRuntimeEventMaxBytes,
	})
	event.PayloadTruncated = true
	return event
}

func projectHostRuntimePayload(kind runtimeevents.EventKind, isACP bool, raw json.RawMessage) (json.RawMessage, string, bool) {
	source, valid := decodeRuntimePayload(raw)
	projected := map[string]any{}
	visibility := "public_metadata"

	switch kind {
	case runtimeevents.KindProcessStarted:
		projected["state"] = "started"
	case runtimeevents.KindProcessExited:
		projected["state"] = "exited"
		copyNumber(projected, "exit_code", source, "exit_code")
		outcome := safeEnum(source["outcome"], "completed", "failed", "canceled", "cancelled", "disconnect", "disconnected", "exited")
		if outcome != "" {
			projected["outcome"] = outcome
		}
		projected["disconnected"] = outcome == "disconnect" || outcome == "disconnected" || source["error"] != nil
	case runtimeevents.KindSessionReady:
		projected["state"] = "ready"
	case runtimeevents.KindSessionIdle:
		projected["state"] = "idle"
	case runtimeevents.KindSessionProcessing:
		projected["state"] = "processing"
	case runtimeevents.KindSessionHeartbeat:
		projected["state"] = "heartbeat"
	case runtimeevents.KindTurnStarted:
		projected["state"] = "started"
	case runtimeevents.KindTurnCompleted:
		usage := publicUsage(source["usage"])
		if len(usage) > 0 {
			projected["usage"] = usage
		}
		// Native emits usage as one completion-shaped event and then emits
		// the actual empty terminal. ACP carries both in one event.
		projected["terminal"] = isACP || len(usage) == 0
	case runtimeevents.KindTurnFailed:
		projected["terminal"] = true
		projected["failed"] = true
	case runtimeevents.KindStdinWrite, runtimeevents.KindStdoutRaw, runtimeevents.KindStderrRaw,
		runtimeevents.KindStdoutLine, runtimeevents.KindStderrLine:
		visibility = "metadata_only"
		projected["content_omitted"] = true
	case runtimeevents.KindAgentDelta:
		visibility = "metadata_only"
		projected["content_omitted"] = true
	case runtimeevents.KindAgentToolUse:
		projected = publicToolUse(source)
	case runtimeevents.KindAgentToolResult:
		projected = publicToolResult(source)
	case runtimeevents.KindAgentSubagentSpawn:
		projected["spawned"] = true
	case runtimeevents.KindAgentPermissionRequested:
		projected["state"] = "requested"
	case runtimeevents.KindAgentPermissionResolved:
		projected["state"] = "resolved"
		if outcome := safeEnum(source["outcome"], "approved", "denied", "canceled", "cancelled"); outcome != "" {
			projected["outcome"] = outcome
		}
	case runtimeevents.KindPolicyNudge, runtimeevents.KindPolicyRewrite,
		runtimeevents.KindPolicyBlock, runtimeevents.KindPolicyApprovalRequested:
		visibility = "observational_metadata"
		projected["observed_only"] = true
	case runtimeevents.KindPlantStarted:
		visibility = "metadata_only"
		projected["state"] = "started"
	case runtimeevents.KindPlantCompleted:
		visibility = "metadata_only"
		projected["state"] = "completed"
	case runtimeevents.KindSandboxApplied:
		visibility = "metadata_only"
		projected["observed"] = true
	case runtimeevents.KindInterruptRequested:
		projected["state"] = "requested"
	case runtimeevents.KindInterruptAcknowledged:
		projected["state"] = "acknowledged"
	default:
		visibility = "unknown_metadata_only"
		projected["unsupported_kind"] = true
		projected["payload_omitted"] = true
	}
	if !valid && len(raw) > 0 {
		projected["payload_invalid"] = true
	}
	return marshalBoundedRuntimePayload(projected, visibility)
}

func decodeRuntimePayload(raw json.RawMessage) (map[string]any, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}, true
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return map[string]any{}, false
	}
	return value, true
}

func publicToolUse(source map[string]any) map[string]any {
	tool := source
	if nested, ok := source["tool_use"].(map[string]any); ok {
		tool = nested
	}
	result := map[string]any{"stage": "started"}
	if id := firstSafeString(tool, "id", "tool_call_id"); id != "" {
		result["tool_id"] = id
	}
	// title is deliberately excluded: several ACPs put the complete command
	// there. Only an explicit semantic tool name is public.
	if name := firstSafeString(tool, "name", "kind"); name != "" {
		result["name"] = name
	}
	if status := publicToolStatus(tool["status"]); status != "" {
		result["status"] = status
	}
	return result
}

func publicToolResult(source map[string]any) map[string]any {
	tool := source
	nested := false
	if value, ok := source["tool_result"].(map[string]any); ok {
		tool = value
		nested = true
	}
	result := map[string]any{}
	if id := firstSafeString(tool, "id", "tool_call_id"); id != "" {
		result["tool_id"] = id
	}
	status := publicToolStatus(tool["status"])
	if status != "" {
		result["status"] = status
	}
	if isError, present := tool["is_error"].(bool); present {
		result["is_error"] = isError
		if isError {
			result["stage"] = "failed"
		} else if nested {
			// Native/Copilot nested result is terminal even when status is
			// absent; flat ACP no-status results are intermediate updates.
			result["stage"] = "completed"
		}
	}
	if result["stage"] == nil {
		switch status {
		case "completed", "success", "succeeded", "done":
			result["stage"] = "completed"
		case "failed", "error":
			result["stage"] = "failed"
		default:
			result["stage"] = "update"
		}
	}
	return result
}

func publicToolStatus(value any) string {
	return safeEnum(value, "pending", "started", "running", "in_progress", "completed", "success", "succeeded", "done", "failed", "error", "canceled", "cancelled")
}

func publicUsage(value any) map[string]any {
	usage, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	result := map[string]any{}
	copyFirstNumber(result, "input_tokens", usage, "input_tokens", "inputTokens", "InputTokens")
	copyFirstNumber(result, "output_tokens", usage, "output_tokens", "outputTokens", "OutputTokens")
	copyFirstNumber(result, "total_tokens", usage, "total_tokens", "totalTokens", "TotalTokens")
	copyFirstNumber(result, "thought_tokens", usage, "thought_tokens", "thoughtTokens", "ThoughtTokens")
	copyFirstNumber(result, "cache_creation_input_tokens", usage, "cache_creation_input_tokens", "cachedWriteTokens", "CacheCreationTokens")
	copyFirstNumber(result, "cache_read_input_tokens", usage, "cache_read_input_tokens", "cachedReadTokens", "CacheReadTokens")
	copyFirstNumber(result, "cached_tokens", usage, "cached_tokens", "CachedTokens")
	copyFirstNumber(result, "cost_usd", usage, "cost_usd", "costUsd", "costUSD", "CostUSD")
	return result
}

func copyFirstNumber(dst map[string]any, dstKey string, src map[string]any, sourceKeys ...string) {
	for _, sourceKey := range sourceKeys {
		if _, ok := src[sourceKey]; !ok {
			continue
		}
		copyNumber(dst, dstKey, src, sourceKey)
		return
	}
}

func copyNumber(dst map[string]any, dstKey string, src map[string]any, srcKey string) {
	switch value := src[srcKey].(type) {
	case float64:
		dst[dstKey] = value
	case int:
		dst[dstKey] = value
	case int64:
		dst[dstKey] = value
	case uint64:
		dst[dstKey] = value
	}
}

func firstSafeString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && value != "" {
			return redactRuntimeString(boundedRuntimeString(value, hostRuntimeStringMaxBytes))
		}
	}
	return ""
}

func safeEnum(value any, allowed ...string) string {
	raw, ok := value.(string)
	if !ok {
		return ""
	}
	raw = strings.ToLower(strings.TrimSpace(raw))
	for _, candidate := range allowed {
		if raw == candidate {
			return raw
		}
	}
	return ""
}

func marshalBoundedRuntimePayload(value map[string]any, visibility string) (json.RawMessage, string, bool) {
	clean := redactRuntimeValue(value, 0)
	raw, err := json.Marshal(clean)
	if err == nil && len(raw) <= hostRuntimePayloadMaxBytes {
		return append(json.RawMessage(nil), raw...), visibility, false
	}
	fallback, _ := json.Marshal(map[string]any{"payload_omitted": true, "size_limit": hostRuntimePayloadMaxBytes})
	return fallback, visibility, true
}

func redactRuntimeValue(value any, depth int) any {
	if depth > 8 {
		return "[DEPTH_LIMIT]"
	}
	switch typed := value.(type) {
	case map[string]any:
		clean := make(map[string]any, len(typed))
		for key, child := range typed {
			if hostRuntimeSecretKey.MatchString(key) {
				clean[key] = "[REDACTED]"
				continue
			}
			clean[key] = redactRuntimeValue(child, depth+1)
		}
		return clean
	case []any:
		if len(typed) > 32 {
			typed = typed[:32]
		}
		clean := make([]any, len(typed))
		for i := range typed {
			clean[i] = redactRuntimeValue(typed[i], depth+1)
		}
		return clean
	case string:
		return redactRuntimeString(boundedRuntimeString(typed, hostRuntimeStringMaxBytes))
	default:
		return typed
	}
}

func redactRuntimeString(value string) string {
	return hostRuntimeSecretValue.ReplaceAllString(value, "[REDACTED]")
}

func boundedRuntimeString(value string, max int) string {
	if max < 1 || len(value) <= max {
		return value
	}
	return value[:max] + "…"
}

func (f *HostRuntimeFeed) String() string {
	return fmt.Sprintf("HostRuntimeFeed(retention=%d, queue=%d)", hostRuntimeFeedRetention, hostRuntimeFeedQueueCapacity)
}
