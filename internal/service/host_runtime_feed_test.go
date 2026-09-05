package service

import (
	"context"
	"encoding/json"
	"path/filepath"
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
			raw:    `{"usage":{"input_tokens":5,"output_tokens":3}}`,
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

func TestHostRuntimeFeedPersistsFIFOWithDistinctHostCursor(t *testing.T) {
	s, err := store.New(context.Background(), filepath.Join(t.TempDir(), "feed.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer func() { _ = s.Close(context.Background()) }()
	feed := NewHostRuntimeFeed(s)

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

func TestHostRuntimeFeedQueueOverflowEmitsGapBeforeNextEvent(t *testing.T) {
	feed := &HostRuntimeFeed{
		store:   &store.Store{},
		queue:   make(chan store.HostRuntimeEvent, 2),
		pending: make(map[hostRuntimeDropKey]*hostRuntimeDrop),
		done:    make(chan struct{}),
	}
	feed.queue <- store.HostRuntimeEvent{Kind: "filler-1"}
	feed.queue <- store.HostRuntimeEvent{Kind: "filler-2"}
	source := func(id string, sequence uint64) runtimeevents.Event {
		return runtimeevents.Event{
			SchemaVersion: runtimeevents.SchemaVersion,
			ID:            id,
			Kind:          runtimeevents.KindSessionHeartbeat,
			Time:          time.Date(2026, 9, 5, 12, 0, int(sequence), 0, time.UTC),
			SessionID:     "session-a",
			Sequence:      sequence,
			Source:        runtimeevents.Source{Channel: runtimeevents.ChannelJSONRPC},
		}
	}
	if err := feed.Publish(context.Background(), "run-a", 1, true, source("dropped", 7)); err != nil {
		t.Fatalf("overflow publish: %v", err)
	}
	<-feed.queue
	<-feed.queue
	if err := feed.Publish(context.Background(), "run-a", 1, true, source("next", 8)); err != nil {
		t.Fatalf("next publish: %v", err)
	}
	gap := <-feed.queue
	next := <-feed.queue
	if gap.Kind != "host_runtime.ingest_gap" || next.SourceEventID != "next" {
		t.Fatalf("post-overflow order = %q then %q, want gap then next", gap.Kind, next.SourceEventID)
	}
	var payload map[string]any
	if err := json.Unmarshal(gap.Payload, &payload); err != nil || payload["dropped_events"] != float64(1) || payload["first_source_sequence"] != float64(7) {
		t.Fatalf("gap payload = %s (%v), want one dropped source sequence 7", gap.Payload, err)
	}
}

func TestRuntimeEventBridgeFeedsBoundSessionAndPreservesLegacyProjection(t *testing.T) {
	s, err := store.New(context.Background(), filepath.Join(t.TempDir(), "bridge-feed.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
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
