package selftools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestCallExecuteTask_ReflexMatch_EmitsUnifiedTraceRecord is the
// CW-20260816-0068 wiring regression, re-based onto TASKS/reflex-taxonomy/
// 06-unified-reflex-telemetry.md's unified sink: matchDispatchToAgentReflex
// (internal/mcp/self_tools_dispatch.go) no longer writes a dedicated
// playbook_match_log row via a ReflexLogger — that field/table/write path
// is retired (no real reader was found; see the task's Work Log). The
// match now surfaces as a single event_log row, written by
// reflexes.EmitFirings, with event_type="dispatch_to_agent",
// category="reflex". This test proves that row exists and carries the
// matched-input audit fact the retired write used to.
//
// Uses a real *store.Store (newTestStore, self_tools_test.go) seeded via
// the real reflexes.SeedBaseReflexes — the same seed data
// dispatch_to_agent_worker_execute lives in — rather than a
// hand-constructed fixture.
func TestCallExecuteTask_ReflexMatch_EmitsUnifiedTraceRecord(t *testing.T) {
	s := newTestStore(t)
	if _, err := reflexes.SeedBaseReflexes(context.Background(), s, nil); err != nil {
		t.Fatalf("SeedBaseReflexes: %v", err)
	}

	spawner := &recordingSpawner{result: &dispatch.SpawnResult{Summary: "worker done"}}
	st := NewSelfToolsTransport(s)
	st.Dispatch = spawner

	// "Implement" is a worker-execute migrated phrase
	// (dispatch_to_agent_worker_execute, seeds.go).
	const msg = "Implement the reflex matcher module"
	const sessionID = "sess-audit-1"
	res, err := st.callExecuteTask(context.Background(), map[string]any{
		"session_id": sessionID,
		"message":    msg,
	})
	if err != nil {
		t.Fatalf("callExecuteTask: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success result, got error: %+v", res)
	}

	events, err := s.ListEvents(context.Background(), "reflex", 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var dispatchEvent *store.EventLog
	for i := range events {
		if events[i].EventType == "dispatch_to_agent" && events[i].SessionID == sessionID {
			dispatchEvent = &events[i]
			break
		}
	}
	if dispatchEvent == nil {
		t.Fatalf("event_log has no dispatch_to_agent row for session %q; got %d reflex-category events", sessionID, len(events))
	}

	var meta map[string]any
	if err := json.Unmarshal([]byte(dispatchEvent.Metadata), &meta); err != nil {
		t.Fatalf("event_log metadata not JSON: %v\nblob: %s", err, dispatchEvent.Metadata)
	}
	if meta["reflex_id"] == "" || meta["reflex_id"] == nil {
		t.Errorf("metadata.reflex_id is empty, want the matched dispatch_to_agent_worker_execute row id")
	}
	spec, ok := meta["spec"].(map[string]any)
	if !ok {
		t.Fatalf("metadata.spec is not a nested object: %#v", meta["spec"])
	}
	if spec["agent_slug"] != "worker" {
		t.Errorf("metadata.spec.agent_slug = %v, want worker", spec["agent_slug"])
	}
	if got := meta["matched_input_excerpt"]; got != msg {
		t.Errorf("metadata.matched_input_excerpt = %v, want %q", got, msg)
	}
}
