package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	runtimeevents "github.com/hollis-labs/go-runtime-events/runtimeevents"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestProjectHostRuntimePayloadHandlesReleasedVocabularyDefaultDeny(t *testing.T) {
	knownKinds := []runtimeevents.EventKind{
		runtimeevents.KindProcessStarted, runtimeevents.KindProcessExited,
		runtimeevents.KindSessionReady, runtimeevents.KindSessionIdle,
		runtimeevents.KindSessionProcessing, runtimeevents.KindSessionHeartbeat,
		runtimeevents.KindTurnStarted, runtimeevents.KindTurnCompleted, runtimeevents.KindTurnFailed,
		runtimeevents.KindStdinWrite, runtimeevents.KindStdoutRaw, runtimeevents.KindStderrRaw,
		runtimeevents.KindStdoutLine, runtimeevents.KindStderrLine,
		runtimeevents.KindAgentDelta, runtimeevents.KindAgentToolUse,
		runtimeevents.KindAgentToolResult, runtimeevents.KindAgentSubagentSpawn,
		runtimeevents.KindAgentPermissionRequested, runtimeevents.KindAgentPermissionResolved,
		runtimeevents.KindPolicyNudge, runtimeevents.KindPolicyRewrite,
		runtimeevents.KindPolicyBlock, runtimeevents.KindPolicyApprovalRequested,
		runtimeevents.KindPlantStarted, runtimeevents.KindPlantCompleted,
		runtimeevents.KindSandboxApplied,
		runtimeevents.KindInterruptRequested, runtimeevents.KindInterruptAcknowledged,
	}
	secretPayload := json.RawMessage(`{
		"prompt":"never expose this prompt",
		"raw_input":{"token":"sk-supersecret123"},
		"result":"private tool result",
		"content":"private model output",
		"error":"authorization=Bearer topsecret",
		"usage":{"input_tokens":11,"output_tokens":7}
	}`)
	for _, kind := range knownKinds {
		t.Run(string(kind), func(t *testing.T) {
			payload, visibility, _ := projectHostRuntimePayload(kind, false, secretPayload)
			if visibility == "unknown_metadata_only" {
				t.Fatalf("released kind %q fell through unknown handling", kind)
			}
			text := string(payload)
			for _, forbidden := range []string{"never expose", "supersecret", "private tool", "private model", "topsecret"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("public payload leaked %q: %s", forbidden, text)
				}
			}
			if len(payload) > hostRuntimePayloadMaxBytes {
				t.Fatalf("payload bytes = %d, exceeds %d", len(payload), hostRuntimePayloadMaxBytes)
			}
		})
	}
	if len(knownKinds) != 29 {
		t.Fatalf("released-kind test inventory = %d, want 29", len(knownKinds))
	}

	payload, visibility, _ := projectHostRuntimePayload(runtimeevents.EventKind("future.kind"), false, secretPayload)
	if visibility != "unknown_metadata_only" || !strings.Contains(string(payload), `"unsupported_kind":true`) {
		t.Fatalf("unknown projection = %s/%s, want metadata-only marker", visibility, payload)
	}
}

func TestMarshalHostRuntimePayloadRecursivelyRedactsAndBounds(t *testing.T) {
	payload, _, truncated := marshalBoundedRuntimePayload(map[string]any{
		"safe": []any{
			map[string]any{"authorization": "Bearer nested-secret", "label": "token=inline-secret"},
			strings.Repeat("x", hostRuntimeStringMaxBytes*2),
		},
	}, "test")
	if truncated {
		t.Fatalf("small redacted payload unexpectedly truncated: %s", payload)
	}
	text := string(payload)
	for _, secret := range []string{"nested-secret", "inline-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("recursive projection leaked %q: %s", secret, text)
		}
	}
	if len(payload) > hostRuntimePayloadMaxBytes || !strings.Contains(text, "[REDACTED]") {
		t.Fatalf("redacted payload = %d bytes %s", len(payload), text)
	}
}

func TestProjectHostRuntimeEventHasHardJSONBound(t *testing.T) {
	event := runtimeevents.Event{
		SchemaVersion: runtimeevents.SchemaVersion,
		ID:            strings.Repeat("\x01", 10_000),
		Kind:          runtimeevents.KindAgentToolUse,
		Time:          time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC),
		SessionID:     strings.Repeat("\x02", 10_000),
		TurnID:        strings.Repeat("\x03", 10_000),
		ParentID:      strings.Repeat("\x04", 10_000),
		Sequence:      1,
		Source: runtimeevents.Source{
			Channel:    runtimeevents.SourceChannel(strings.Repeat("\x05", 10_000)),
			Confidence: runtimeevents.ConfidenceExact,
		},
		Process: runtimeevents.Process{
			Provider:          strings.Repeat("\x06", 10_000),
			Runtime:           strings.Repeat("\x07", 10_000),
			ProviderSessionID: strings.Repeat("\x08", 10_000),
		},
		Payload: json.RawMessage(`{"tool_use":{"id":"tool","name":"read_file"}}`),
	}
	projected := projectHostRuntimeEvent("run-a", 1, false, event)
	raw, err := json.Marshal(projected)
	if err != nil {
		t.Fatalf("marshal bounded projection: %v", err)
	}
	if len(raw) > hostRuntimeEventMaxBytes {
		t.Fatalf("public event bytes = %d, exceeds hard cap %d", len(raw), hostRuntimeEventMaxBytes)
	}
}

func TestProjectHostRuntimePayloadNormalizesNativeAndACPToolsAndCompletion(t *testing.T) {
	tests := []struct {
		name   string
		kind   runtimeevents.EventKind
		acp    bool
		raw    string
		checks map[string]any
	}{
		{
			name:   "nested tool use wins once",
			kind:   runtimeevents.KindAgentToolUse,
			raw:    `{"tool_call_id":"flat","tool_use":{"id":"nested","name":"read_file","input":{"path":"/secret"}}}`,
			checks: map[string]any{"tool_id": "nested", "name": "read_file", "stage": "started"},
		},
		{
			name:   "flat no-status ACP result is update",
			kind:   runtimeevents.KindAgentToolResult,
			acp:    true,
			raw:    `{"tool_call_id":"call-1","raw_output":"secret"}`,
			checks: map[string]any{"tool_id": "call-1", "stage": "update"},
		},
		{
			name:   "nested native result is terminal",
			kind:   runtimeevents.KindAgentToolResult,
			raw:    `{"tool_result":{"id":"call-2","is_error":false,"content_preview":"secret"}}`,
			checks: map[string]any{"tool_id": "call-2", "stage": "completed", "is_error": false},
		},
		{
			name:   "explicit ACP failure is terminal",
			kind:   runtimeevents.KindAgentToolResult,
			acp:    true,
			raw:    `{"tool_call_id":"call-3","status":"failed","result":"secret"}`,
			checks: map[string]any{"tool_id": "call-3", "stage": "failed", "status": "failed"},
		},
		{
			name:   "native usage completion is not terminal",
			kind:   runtimeevents.KindTurnCompleted,
			raw:    `{"usage":{"InputTokens":5,"OutputTokens":3,"StopReason":"end_turn"}}`,
			checks: map[string]any{"terminal": false},
		},
		{
			name:   "ACP usage completion is terminal",
			kind:   runtimeevents.KindTurnCompleted,
			acp:    true,
			raw:    `{"usage":{"inputTokens":5,"outputTokens":3,"totalTokens":8}}`,
			checks: map[string]any{"terminal": true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, _, _ := projectHostRuntimePayload(tc.kind, tc.acp, json.RawMessage(tc.raw))
			var got map[string]any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("decode projection: %v", err)
			}
			for key, want := range tc.checks {
				if gotValue := got[key]; gotValue != want {
					t.Errorf("%s = %#v, want %#v (payload %s)", key, gotValue, want, raw)
				}
			}
			if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "/secret") {
				t.Fatalf("tool projection leaked private data: %s", raw)
			}
		})
	}
}

func TestPublicUsageNormalizesReleasedAdapterShapes(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want map[string]any
	}{
		{
			name: "claude ACP standard camelCase",
			raw:  `{"inputTokens":11,"outputTokens":7,"totalTokens":23,"thoughtTokens":2,"cachedReadTokens":2,"cachedWriteTokens":1}`,
			want: map[string]any{"input_tokens": float64(11), "output_tokens": float64(7), "total_tokens": float64(23), "thought_tokens": float64(2), "cache_read_input_tokens": float64(2), "cache_creation_input_tokens": float64(1)},
		},
		{name: "codex and opencode fixture total", raw: `{"totalTokens":5}`, want: map[string]any{"total_tokens": float64(5)}},
		{name: "native Go JSON", raw: `{"InputTokens":3,"OutputTokens":2,"CacheCreationTokens":1,"CacheReadTokens":4}`, want: map[string]any{"input_tokens": float64(3), "output_tokens": float64(2), "cache_creation_input_tokens": float64(1), "cache_read_input_tokens": float64(4)}},
		{name: "pi and copilot no usage", raw: `{}`, want: map[string]any{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var decoded map[string]any
			if err := json.Unmarshal([]byte(tc.raw), &decoded); err != nil {
				t.Fatal(err)
			}
			if got := publicUsage(decoded); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("publicUsage(%s) = %#v, want %#v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestHostRuntimeFeedPersistsFIFOWithDistinctHostCursor(t *testing.T) {
	s, openErr := store.New(context.Background(), filepath.Join(t.TempDir(), "feed.db"))
	if openErr != nil {
		t.Fatalf("new store: %v", openErr)
	}
	defer func() { _ = s.Close(context.Background()) }()
	feed := NewHostRuntimeFeed(s)
	if generation, err := feed.ReserveRuntimeGeneration(context.Background(), "session-a", "run-a"); err != nil || generation != 1 {
		t.Fatalf("reserve runtime run = %d, %v", generation, err)
	}

	for i, kind := range []runtimeevents.EventKind{runtimeevents.KindSessionReady, runtimeevents.KindProcessStarted, runtimeevents.KindSessionProcessing} {
		event := runtimeevents.Event{
			SchemaVersion: runtimeevents.SchemaVersion,
			ID:            "source-" + string(rune('a'+i)),
			Kind:          kind,
			Time:          time.Date(2026, 9, 5, 12, 0, i, 0, time.UTC),
			SessionID:     "session-a",
			Sequence:      uint64(i + 9),
			Source:        runtimeevents.Source{Channel: runtimeevents.ChannelJSONRPC},
		}
		if err := feed.Publish(context.Background(), "run-a", 1, true, event); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := feed.Close(closeCtx); err != nil {
		t.Fatalf("close feed: %v", err)
	}
	replay, err := s.HostRuntimeEventsAfter(context.Background(), "session-a", 0, 10)
	if err != nil || len(replay.Events) != 3 {
		t.Fatalf("replay = %+v, %v", replay, err)
	}
	for i, event := range replay.Events {
		if event.Cursor != int64(i+1) || event.SourceSequence != uint64(i+9) {
			t.Errorf("event %d cursor/source sequence = %d/%d", i, event.Cursor, event.SourceSequence)
		}
	}
}

func TestHostRuntimeFeedQueueOverflowIsSessionOrderedAcrossRuns(t *testing.T) {
	_, cancelWorker := context.WithCancel(context.Background())
	feed := &HostRuntimeFeed{
		store:        &store.Store{},
		queue:        make(chan store.HostRuntimeEvent, 2),
		pending:      make(map[string]*hostRuntimeDrop),
		cancelWorker: cancelWorker,
		done:         make(chan struct{}),
	}
	feed.queue <- store.HostRuntimeEvent{Kind: "filler-1"}
	feed.queue <- store.HostRuntimeEvent{Kind: "filler-2"}
	source := func(id string, sequence uint64) runtimeevents.Event {
		second := int(sequence) //nolint:gosec // Test fixtures pass fixed single-digit sequence values.
		return runtimeevents.Event{
			SchemaVersion: runtimeevents.SchemaVersion,
			ID:            id,
			Kind:          runtimeevents.KindSessionHeartbeat,
			Time:          time.Date(2026, 9, 5, 12, 0, second, 0, time.UTC),
			SessionID:     "session-a",
			Sequence:      sequence,
			Source:        runtimeevents.Source{Channel: runtimeevents.ChannelJSONRPC},
		}
	}
	if err := feed.Publish(context.Background(), "run-b", 2, true, source("dropped-successor", 7)); err != nil {
		t.Fatalf("successor overflow publish: %v", err)
	}
	if err := feed.Publish(context.Background(), "run-a", 1, true, source("dropped-delayed-predecessor", 8)); err != nil {
		t.Fatalf("predecessor overflow publish: %v", err)
	}
	<-feed.queue
	<-feed.queue
	if err := feed.Publish(context.Background(), "run-c", 3, true, source("next", 1)); err != nil {
		t.Fatalf("next publish: %v", err)
	}
	gap := <-feed.queue
	next := <-feed.queue
	if gap.Kind != "host_runtime.ingest_gap" || gap.RuntimeGeneration != 2 || gap.RuntimeRunID != "run-b" || next.SourceEventID != "next" || next.RuntimeGeneration != 3 {
		t.Fatalf("post-overflow order = %q then %q, want gap then next", gap.Kind, next.SourceEventID)
	}
	var payload map[string]any
	if err := json.Unmarshal(gap.Payload, &payload); err != nil || payload["dropped_events"] != float64(2) || payload["spans_runtime_runs"] != true {
		t.Fatalf("gap payload = %s (%v), want two dropped events spanning runs", gap.Payload, err)
	}
}

func TestHostRuntimeLossLedgerHasHardSessionBound(t *testing.T) {
	for _, ledgerName := range []string{"publisher pending", "worker failures"} {
		t.Run(ledgerName, func(t *testing.T) {
			ledger := make(map[string]*hostRuntimeDrop)
			for i := 0; i < hostRuntimeLossLedgerMaxSessions; i++ {
				event := store.HostRuntimeEvent{SessionID: fmt.Sprintf("session-%03d", i), RuntimeRunID: "run", RuntimeGeneration: 1}
				if err := addHostRuntimeDrop(ledger, event); err != nil {
					t.Fatalf("add bounded ledger entry %d: %v", i, err)
				}
			}
			if err := addHostRuntimeDrop(ledger, store.HostRuntimeEvent{SessionID: "session-overflow", RuntimeRunID: "run", RuntimeGeneration: 1}); !errors.Is(err, errHostRuntimeLossLedgerSaturated) {
				t.Fatalf("overflow error = %v", err)
			}
			if len(ledger) != hostRuntimeLossLedgerMaxSessions {
				t.Fatalf("loss ledger size = %d, want hard cap %d", len(ledger), hostRuntimeLossLedgerMaxSessions)
			}
		})
	}
}

func TestHostRuntimeLossCountSaturates(t *testing.T) {
	drop := hostRuntimeDrop{count: math.MaxInt64 - 1, firstRunID: "run-a", lastRunID: "run-a", maxGeneration: 1, maxGenerationRunID: "run-a"}
	mergeHostRuntimeDrop(&drop, hostRuntimeDrop{count: 2, firstRunID: "run-b", lastRunID: "run-b", maxGeneration: 2, maxGenerationRunID: "run-b"})
	if drop.count != math.MaxInt64 || !drop.countTruncated || !drop.spansRuns || drop.maxGenerationRunID != "run-b" {
		t.Fatalf("saturated drop = %+v", drop)
	}
}

func TestRuntimeEventBridgeReservationExhaustionPreservesLegacyProjection(t *testing.T) {
	s, err := store.New(context.Background(), filepath.Join(t.TempDir(), "reserve-exhausted.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(context.Background()) }()
	feed := NewHostRuntimeFeed(s)
	calls := 0
	feed.reserveFn = func(context.Context, string, string) (int64, error) {
		calls++
		return 0, errors.New("persistent reservation failure")
	}
	streams := NewStreamManager()
	producer := streams.CreateStream("message-exhausted", "session-exhausted")
	defer close(producer)
	legacy, _, ok := streams.Subscribe("message-exhausted", 0)
	if !ok {
		t.Fatal("legacy stream unavailable")
	}
	sink := newRuntimeEventBridgeSink(&agentEventBridge{streams: streams, runtimeFeed: feed}, "session-exhausted", true)
	if calls != hostRuntimeGenerationReserveAttempts || sink.runGeneration != 0 {
		t.Fatalf("reservation calls/generation = %d/%d", calls, sink.runGeneration)
	}
	err = sink.Write(context.Background(), runtimeevents.Event{
		SchemaVersion: runtimeevents.SchemaVersion, ID: "source-delta", Kind: runtimeevents.KindAgentDelta,
		Time: time.Now(), SessionID: "session-exhausted", Sequence: 1,
		Payload: json.RawMessage(`{"content":"legacy survives"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "generation was not reserved") {
		t.Fatalf("feed admission error = %v", err)
	}
	select {
	case event := <-legacy:
		if event.Type != "delta" || event.Content != "legacy survives" {
			t.Fatalf("legacy event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("legacy projection was blocked by runtime feed reservation failure")
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := feed.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
}

func TestHostRuntimePersistenceFailureGapPrecedesSuccessorRun(t *testing.T) {
	s, openErr := store.New(context.Background(), filepath.Join(t.TempDir(), "persist-order.db"))
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer func() { _ = s.Close(context.Background()) }()
	feed := NewHostRuntimeFeed(s)
	genA, err := feed.ReserveRuntimeGeneration(context.Background(), "session-order", "run-a")
	if err != nil {
		t.Fatal(err)
	}
	genB, err := feed.ReserveRuntimeGeneration(context.Background(), "session-order", "run-b")
	if err != nil {
		t.Fatal(err)
	}
	originalPersist := feed.persistFn
	failedFirst := false
	var attempts []string
	feed.persistFn = func(ctx context.Context, event store.HostRuntimeEvent) (bool, error) {
		attempts = append(attempts, event.Kind+":"+event.RuntimeRunID)
		if !failedFirst {
			failedFirst = true
			return false, errors.New("injected one-shot write failure")
		}
		return originalPersist(ctx, event)
	}
	source := func(id string) runtimeevents.Event {
		return runtimeevents.Event{
			SchemaVersion: runtimeevents.SchemaVersion, ID: id, Kind: runtimeevents.KindSessionReady,
			Time: time.Now(), SessionID: "session-order", Sequence: 1,
			Source: runtimeevents.Source{Channel: runtimeevents.ChannelJSONRPC},
		}
	}
	if publishErr := feed.Publish(context.Background(), "run-a", genA, true, source("a-ready")); publishErr != nil {
		t.Fatal(publishErr)
	}
	if publishErr := feed.Publish(context.Background(), "run-b", genB, true, source("b-ready")); publishErr != nil {
		t.Fatal(publishErr)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if closeErr := feed.Close(closeCtx); closeErr != nil {
		t.Fatal(closeErr)
	}
	wantAttempts := []string{"session.ready:run-a", "host_runtime.ingest_gap:run-a", "session.ready:run-b"}
	if fmt.Sprint(attempts) != fmt.Sprint(wantAttempts) {
		t.Fatalf("persistence attempts = %v, want %v", attempts, wantAttempts)
	}
	replay, err := s.HostRuntimeEventsAfter(context.Background(), "session-order", 0, 10)
	if err != nil || len(replay.Events) != 2 || replay.Events[0].Kind != "host_runtime.ingest_gap" || replay.Events[1].RuntimeRunID != "run-b" {
		t.Fatalf("ordered persistence replay = %+v, %v", replay, err)
	}
}

func TestHostRuntimeCloseCancelsAndJoinsBlockedPersistence(t *testing.T) {
	s, err := store.New(context.Background(), filepath.Join(t.TempDir(), "blocked-close.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(context.Background()) }()
	feed := NewHostRuntimeFeed(s)
	gen, err := feed.ReserveRuntimeGeneration(context.Background(), "session-close", "run-close")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	returned := make(chan struct{})
	feed.persistFn = func(ctx context.Context, _ store.HostRuntimeEvent) (bool, error) {
		close(started)
		<-ctx.Done()
		close(returned)
		return false, ctx.Err()
	}
	if err := feed.Publish(context.Background(), "run-close", gen, true, runtimeevents.Event{
		SchemaVersion: runtimeevents.SchemaVersion, ID: "blocked", Kind: runtimeevents.KindSessionReady,
		Time: time.Now(), SessionID: "session-close", Sequence: 1,
	}); err != nil {
		t.Fatal(err)
	}
	<-started
	closeCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := feed.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close error = %v, want deadline", err)
	}
	select {
	case <-returned:
	default:
		t.Fatal("Close returned before blocked persistence exited")
	}
	select {
	case <-feed.done:
	default:
		t.Fatal("Close returned before worker joined")
	}
	if err := s.DB.PingContext(context.Background()); err != nil {
		t.Fatalf("store unusable after joined close: %v", err)
	}
}

func TestHostRuntimeCloseCancelsReservationAndClosesAdmission(t *testing.T) {
	s, err := store.New(context.Background(), filepath.Join(t.TempDir(), "reserve-close.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(context.Background()) }()
	feed := NewHostRuntimeFeed(s)
	started := make(chan struct{})
	feed.reserveFn = func(ctx context.Context, _, _ string) (int64, error) {
		close(started)
		<-ctx.Done()
		return 0, ctx.Err()
	}
	reserveDone := make(chan error, 1)
	go func() {
		_, err := feed.ReserveRuntimeGeneration(context.Background(), "session-reserve", "run-reserve")
		reserveDone <- err
	}()
	<-started
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := feed.Close(closeCtx); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := <-reserveDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("reservation error = %v, want canceled", err)
	}
	if _, err := feed.ReserveRuntimeGeneration(context.Background(), "session-after", "run-after"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("post-close reservation error = %v", err)
	}
}

func TestRuntimeEventBridgeRetriesTransientGenerationReservation(t *testing.T) {
	s, err := store.New(context.Background(), filepath.Join(t.TempDir(), "reserve-retry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(context.Background()) }()
	feed := NewHostRuntimeFeed(s)
	originalReserve := feed.reserveFn
	calls := 0
	feed.reserveFn = func(ctx context.Context, sessionID, runID string) (int64, error) {
		calls++
		if calls < hostRuntimeGenerationReserveAttempts {
			return 0, errors.New("transient reservation failure")
		}
		return originalReserve(ctx, sessionID, runID)
	}
	sink := newRuntimeEventBridgeSink(&agentEventBridge{runtimeFeed: feed}, "session-retry", true)
	if calls != hostRuntimeGenerationReserveAttempts || sink.runGeneration != 1 {
		t.Fatalf("reservation calls/generation = %d/%d", calls, sink.runGeneration)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := feed.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeEventBridgeFeedsBoundSessionAndPreservesLegacyProjection(t *testing.T) {
	s, openErr := store.New(context.Background(), filepath.Join(t.TempDir(), "bridge-feed.db"))
	if openErr != nil {
		t.Fatalf("new store: %v", openErr)
	}
	defer func() { _ = s.Close(context.Background()) }()
	feed := NewHostRuntimeFeed(s)
	streams := NewStreamManager()
	producer := streams.CreateStream("message-a", "session-bound")
	defer close(producer)
	legacy, _, ok := streams.Subscribe("message-a", 0)
	if !ok {
		t.Fatal("legacy stream unavailable")
	}
	sink := newRuntimeEventBridgeSink(&agentEventBridge{streams: streams, runtimeFeed: feed}, "session-bound", true)
	if err := sink.Write(context.Background(), runtimeevents.Event{
		SchemaVersion: runtimeevents.SchemaVersion,
		ID:            "source-delta",
		Kind:          runtimeevents.KindAgentDelta,
		Time:          time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC),
		SessionID:     "malformed-cross-session",
		Sequence:      1,
		Source:        runtimeevents.Source{Channel: runtimeevents.ChannelJSONRPC},
		Payload:       json.RawMessage(`{"content":"legacy-visible but feed-omitted"}`),
	}); err != nil {
		t.Fatalf("sink write: %v", err)
	}
	select {
	case event := <-legacy:
		if event.Type != "delta" || event.Content != "legacy-visible but feed-omitted" {
			t.Fatalf("legacy event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("legacy projection did not receive canonical delta")
	}
	successor := newRuntimeEventBridgeSink(&agentEventBridge{streams: streams, runtimeFeed: feed}, "session-bound", true)
	if successor.runGeneration != sink.runGeneration+1 {
		t.Fatalf("successor generation = %d, want %d", successor.runGeneration, sink.runGeneration+1)
	}
	if err := successor.Write(context.Background(), runtimeevents.Event{
		SchemaVersion: runtimeevents.SchemaVersion,
		ID:            "successor-ready",
		Kind:          runtimeevents.KindSessionReady,
		Time:          time.Date(2026, 9, 5, 12, 0, 1, 0, time.UTC),
		SessionID:     "session-bound",
		Sequence:      1,
		Source:        runtimeevents.Source{Channel: runtimeevents.ChannelJSONRPC},
	}); err != nil {
		t.Fatalf("successor sink write: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := feed.Close(ctx); err != nil {
		t.Fatalf("close feed: %v", err)
	}
	replay, err := s.HostRuntimeEventsAfter(context.Background(), "session-bound", 0, 10)
	if err != nil || len(replay.Events) != 2 {
		t.Fatalf("bound replay = %+v, %v", replay, err)
	}
	if strings.Contains(string(replay.Events[0].Payload), "legacy-visible") {
		t.Fatalf("public delta leaked model content: %s", replay.Events[0].Payload)
	}
	if replay.Events[0].RuntimeGeneration != 1 || replay.Events[1].RuntimeGeneration != 2 || replay.Events[1].SourceSequence != 1 {
		t.Fatalf("runtime generations/source reset = %+v, want generations 1,2 and successor source sequence 1", replay.Events)
	}
	cross, err := s.HostRuntimeEventsAfter(context.Background(), "malformed-cross-session", 0, 10)
	if err != nil || len(cross.Events) != 0 {
		t.Fatalf("source-claimed session received public event: %+v, %v", cross, err)
	}
}
