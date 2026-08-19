package mcp

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/store"
)

// capturingReflexLogger records every store.ReflexMatchLogEntry passed to
// LogReflexMatch, verbatim, before store.Store.LogReflexMatch's own
// identical-pair collapse (the bloat-avoidance behavior
// TestLogReflexMatch_RawSentTextIdentical_NotDuplicated in
// internal/store already covers). Using a capturing fake here — instead
// of reading back through the real store — isolates what this file's
// own code (matchDispatchToAgentReflex) is responsible for (setting
// RawInputText/SentInputText correctly on the way in) from what
// store.LogReflexMatch is responsible for (collapsing them at
// persistence time), rather than conflating the two under one
// assertion.
type capturingReflexLogger struct {
	captured []store.ReflexMatchLogEntry
}

func (l *capturingReflexLogger) LogReflexMatch(entry store.ReflexMatchLogEntry) error {
	l.captured = append(l.captured, entry)
	return nil
}

// TestCallExecuteTask_ReflexMatch_LogsRawAndSentInputText is the
// CW-20260816-0068 wiring regression, re-based (TASKS/phase-4/
// 03-migrate-promptrouter-to-reflexes.md) onto the DB-backed
// dispatch_to_agent reflex path: matchDispatchToAgentReflex
// (internal/mcp/self_tools_dispatch.go) must populate the
// store.ReflexMatchLogEntry's RawInputText/SentInputText fields from the
// same two variables the dispatch call itself uses — message (raw user
// input) and dispatchMessage (the text actually sent to
// dispatch.ExecuteTask, which E2 grounding may rewrite before this call
// site is reached). With no GroundingRecaller configured here,
// dispatchMessage never diverges from message, so both fields must come
// through identical to the raw message — proving the call site reads
// from the correct variables rather than, say, leaving the new fields
// zero-valued.
//
// Uses a real *store.Store (newTestStore, self_tools_test.go) for
// st.Store, seeded via the real reflexes.SeedBaseReflexes — the same
// seed data TASKS/phase-4/03's migrated worker-execute reflex
// (dispatch_to_agent_worker_execute) lives in — rather than a
// hand-constructed promptrouter.Reflex fixture, since the reflex catalog
// is DB-backed now, not an in-memory ReflexSet field. st.ReflexLogger is
// a separate capturing fake (see above), independent of st.Store, so
// this test observes the entry exactly as this file's code built it.
func TestCallExecuteTask_ReflexMatch_LogsRawAndSentInputText(t *testing.T) {
	s := newTestStore(t)
	if _, err := reflexes.SeedBaseReflexes(context.Background(), s, nil); err != nil {
		t.Fatalf("SeedBaseReflexes: %v", err)
	}

	spawner := &recordingSpawner{result: &dispatch.SpawnResult{Summary: "worker done"}}
	logger := &capturingReflexLogger{}
	st := NewSelfToolsTransport(s)
	st.Dispatch = spawner
	st.ReflexLogger = logger

	// "Implement" is a worker-execute migrated phrase
	// (dispatch_to_agent_worker_execute, seeds.go) — matches the same
	// intent as the message this test used before the migration.
	const msg = "Implement the reflex matcher module"
	res, err := st.callExecuteTask(context.Background(), map[string]any{
		"session_id": "sess-audit-1",
		"message":    msg,
	})
	if err != nil {
		t.Fatalf("callExecuteTask: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success result, got error: %+v", res)
	}
	if len(logger.captured) != 1 {
		t.Fatalf("expected 1 logged reflex match, got %d", len(logger.captured))
	}

	entry := logger.captured[0]
	if entry.ReflexID == "" {
		t.Errorf("ReflexID is empty, want the matched dispatch_to_agent_worker_execute row id")
	}
	if entry.ProfileSlug != "worker" {
		t.Errorf("ProfileSlug = %q, want worker", entry.ProfileSlug)
	}
	if entry.RawInputText != msg {
		t.Errorf("RawInputText = %q, want %q", entry.RawInputText, msg)
	}
	if entry.SentInputText != msg {
		t.Errorf("SentInputText = %q, want %q", entry.SentInputText, msg)
	}
	if entry.RawInputText != entry.SentInputText {
		t.Errorf("expected RawInputText == SentInputText with no grounding rewrite, got %q vs %q",
			entry.RawInputText, entry.SentInputText)
	}
}
