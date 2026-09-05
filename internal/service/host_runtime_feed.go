package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	runtimeevents "github.com/hollis-labs/go-runtime-events/runtimeevents"
	"github.com/hollis-labs/nanite/internal/store"
)

const (
	hostRuntimeFeedQueueCapacity = 1024
	hostRuntimeFeedRetention     = 512
	hostRuntimePayloadMaxBytes   = 4096
	hostRuntimeEventMaxBytes     = store.HostRuntimeFeedMaxEventBytes
	hostRuntimeStringMaxBytes    = 384
)

var (
	hostRuntimeSecretValue = regexp.MustCompile(`(?i)(bearer\s+[^\s,;]+|sk-[a-z0-9_-]{8,}|gh[pousr]_[a-z0-9_]{8,}|(?:api[_-]?key|token|secret|password|authorization|cookie|credential)\s*[:=]\s*[^\s,;]+)`)
	hostRuntimeSecretKey   = regexp.MustCompile(`(?i)^(api.?key|token|secret|password|authorization|cookie|credential|private.?key|raw.?input|arguments?|args|prompt|stdin|stdout|stderr|result|output|content|bytes|terminal.?output)$`)
)

// HostRuntimeFeed owns the bounded ingestion FIFO between wrapper IO and the
// durable public feed. Wrapper sinks never perform SQLite or network IO.
// A single FIFO also gives overlapping old/new wrapper runs for one session a
// deterministic host order; runtime_run_id lets reducers ignore stale exits.
type HostRuntimeFeed struct {
	store *store.Store

	mu      sync.Mutex
	closed  bool
	queue   chan store.HostRuntimeEvent
	pending map[hostRuntimeDropKey]*hostRuntimeDrop
	done    chan struct{}
}

type hostRuntimeDropKey struct {
	sessionID  string
	runID      string
	generation int64
}

type hostRuntimeDrop struct {
	count         int64
	firstSequence uint64
	lastSequence  uint64
}

func NewHostRuntimeFeed(s *store.Store) *HostRuntimeFeed {
	f := &HostRuntimeFeed{
		store:   s,
		queue:   make(chan store.HostRuntimeEvent, hostRuntimeFeedQueueCapacity),
		pending: make(map[hostRuntimeDropKey]*hostRuntimeDrop),
		done:    make(chan struct{}),
	}
	go f.run()
	return f
}

func (f *HostRuntimeFeed) ReserveRuntimeGeneration(ctx context.Context, sessionID string) (int64, error) {
	if f == nil || f.store == nil {
		return 0, errors.New("host runtime feed is unavailable")
	}
	f.mu.Lock()
	closed := f.closed
	f.mu.Unlock()
	if closed {
		return 0, errors.New("host runtime feed is closed")
	}
	return f.store.ReserveHostRuntimeGeneration(ctx, sessionID)
}

// Publish projects and copies ev before returning, then admits it without
// blocking the wrapper runtime. Queue overflow is accumulated per runtime run
// and becomes a durable host_runtime.ingest_gap record before the next event.
func (f *HostRuntimeFeed) Publish(_ context.Context, runtimeRunID string, runtimeGeneration int64, isACP bool, ev runtimeevents.Event) error {
	if f == nil || f.store == nil {
		return nil
	}
	if runtimeRunID == "" || runtimeGeneration < 1 {
		return errors.New("host runtime feed run identity is incomplete")
	}
	projected := projectHostRuntimeEvent(runtimeRunID, runtimeGeneration, isACP, ev)
	key := hostRuntimeDropKey{sessionID: projected.SessionID, runID: projected.RuntimeRunID, generation: projected.RuntimeGeneration}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return errors.New("host runtime feed is closed")
	}
	if pending := f.pending[key]; pending != nil {
		select {
		case f.queue <- newHostRuntimeIngestGap(key, *pending):
			delete(f.pending, key)
		default:
			f.noteDropLocked(key, projected.SourceSequence)
			return nil
		}
	}
	select {
	case f.queue <- projected:
	default:
		f.noteDropLocked(key, projected.SourceSequence)
	}
	return nil
}

func (f *HostRuntimeFeed) noteDropLocked(key hostRuntimeDropKey, sequence uint64) {
	drop := f.pending[key]
	if drop == nil {
		drop = &hostRuntimeDrop{firstSequence: sequence}
		f.pending[key] = drop
	}
	drop.count++
	drop.lastSequence = sequence
}

func newHostRuntimeIngestGap(key hostRuntimeDropKey, drop hostRuntimeDrop) store.HostRuntimeEvent {
	payload, _ := json.Marshal(map[string]any{
		"dropped_events":        drop.count,
		"first_source_sequence": drop.firstSequence,
		"last_source_sequence":  drop.lastSequence,
	})
	event := store.HostRuntimeEvent{
		SchemaVersion:     store.HostRuntimeFeedSchemaVersion,
		SessionID:         key.sessionID,
		RuntimeRunID:      key.runID,
		RuntimeGeneration: key.generation,
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
	failed := make(map[hostRuntimeDropKey]*hostRuntimeDrop)
	for event := range f.queue {
		key := hostRuntimeDropKey{sessionID: event.SessionID, runID: event.RuntimeRunID, generation: event.RuntimeGeneration}
		if pending := failed[key]; pending != nil {
			if err := f.persist(newHostRuntimeIngestGap(key, *pending)); err != nil {
				mergeHostRuntimeDrop(pending, dropFromHostRuntimeEvent(event))
				slog.Warn("host runtime feed: persist recovery gap", "session_id", event.SessionID, "runtime_run_id", event.RuntimeRunID, "err", err)
				continue
			}
			delete(failed, key)
		}
		if err := f.persist(event); err != nil {
			drop := failed[key]
			if drop == nil {
				drop = &hostRuntimeDrop{}
				failed[key] = drop
			}
			mergeHostRuntimeDrop(drop, dropFromHostRuntimeEvent(event))
			slog.Warn("host runtime feed: persist event", "session_id", event.SessionID, "runtime_run_id", event.RuntimeRunID, "kind", event.Kind, "err", err)
		}
	}
	for key, pending := range failed {
		if err := f.persist(newHostRuntimeIngestGap(key, *pending)); err != nil {
			slog.Warn("host runtime feed: final persistence gap unavailable", "session_id", key.sessionID, "runtime_run_id", key.runID, "dropped_events", pending.count, "err", err)
		}
	}
}

func (f *HostRuntimeFeed) persist(event store.HostRuntimeEvent) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_, inserted, err := f.store.AppendHostRuntimeEvent(ctx, event, hostRuntimeFeedRetention)
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
		return hostRuntimeDrop{count: 1, firstSequence: event.SourceSequence, lastSequence: event.SourceSequence}
	}
	var payload struct {
		Count int64  `json:"dropped_events"`
		First uint64 `json:"first_source_sequence"`
		Last  uint64 `json:"last_source_sequence"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil || payload.Count < 1 {
		return hostRuntimeDrop{count: 1}
	}
	return hostRuntimeDrop{count: payload.Count, firstSequence: payload.First, lastSequence: payload.Last}
}

func mergeHostRuntimeDrop(dst *hostRuntimeDrop, incoming hostRuntimeDrop) {
	if incoming.count < 1 {
		return
	}
	if dst.count == 0 {
		dst.firstSequence = incoming.firstSequence
	}
	dst.count += incoming.count
	dst.lastSequence = incoming.lastSequence
}

// Close stops admission, flushes any queued overflow markers, and drains all
// admitted work before returning or ctx expires. It is called after Chat has
// stopped wrapper producers and before the process closes the Store.
func (f *HostRuntimeFeed) Close(ctx context.Context) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	var flushErr error
	if !f.closed {
		f.closed = true
	flushPending:
		for key, drop := range f.pending {
			select {
			case f.queue <- newHostRuntimeIngestGap(key, *drop):
				delete(f.pending, key)
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
	if flushErr != nil {
		return flushErr
	}
	select {
	case <-f.done:
		return nil
	case <-ctx.Done():
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
	copyFirstNumber(result, "input_tokens", usage, "input_tokens", "InputTokens")
	copyFirstNumber(result, "output_tokens", usage, "output_tokens", "OutputTokens")
	copyFirstNumber(result, "total_tokens", usage, "total_tokens", "TotalTokens")
	copyFirstNumber(result, "cache_creation_input_tokens", usage, "cache_creation_input_tokens", "CacheCreationTokens")
	copyFirstNumber(result, "cache_read_input_tokens", usage, "cache_read_input_tokens", "CacheReadTokens")
	copyFirstNumber(result, "cached_tokens", usage, "cached_tokens", "CachedTokens")
	copyFirstNumber(result, "cost_usd", usage, "cost_usd", "CostUSD")
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
