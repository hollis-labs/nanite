package service

// TASKS/reflex-taxonomy/08-fix-resolve-fail-open-visibility.md item 3: a
// canary log for the same kind-lookup-failure fail-open Resolve() itself
// now Warn-logs directly (resolve.go) — when the dispatch_to_agent kind's
// combining_algorithm degrades to all_applicable (instead of its seeded
// first_applicable), more than one candidate can be selected in the same
// pass, but attemptReflexDispatch only ever reads
// resolved.FiredReflexes[0]. This test proves that degraded case is
// itself operator-visible: two real dispatch_to_agent reflex rows whose
// triggers both fire, with the reflex_action_kinds row for
// dispatch_to_agent removed (the same kind-lookup-failure fail-open
// resolve_test.go's TestResolve_KindLookupFailure_LogsWarning exercises
// at the primitive level), produces the "resolved multiple candidates"
// Warn log line at this call site.

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestAttemptReflexDispatch_KindLookupDegraded_LogsMultiCandidateCanary(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reflex-dispatch-multi-candidate.db")
	st, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close(context.

			// Two real dispatch_to_agent rows, both matching the same
			// user_regex_window trigger — same pattern
			// chat_reflex_dispatch_cooldown_test.go's fixture uses.
			Background())
	})

	for i, name := range []string{"dispatch_multi_probe_a", "dispatch_multi_probe_b"} {
		if _, err := st.InsertAgentReflex(context.Background(), store.AgentReflex{
			ClassTag:    "advisor",
			Name:        name,
			TriggerKind: store.ReflexTriggerPredicate,
			TriggerSpec: `{"kind":"user_regex_window","window":1,"pattern":"probe-multi-candidate-token"}`,
			ActionKind:  store.ReflexActionDispatchToAgent,
			ActionSpec:  `{"agent_slug":"planner","confidence":0.9,"reason":"multi-candidate canary test"}`,
			Priority:    int64(50 - i), // distinct priorities, deterministic ordering
		}); err != nil {
			t.Fatalf("InsertAgentReflex(%s): %v", name, err)
		}
	}

	// Remove dispatch_to_agent's reflex_action_kinds row entirely — the
	// same "kind lookup fails" cause TestResolve_KindLookupFailure_
	// LogsWarning exercises directly against Resolve() (engine.go's
	// kindLookup closure's own "not cached" error is this same shape).
	// Seeded 'first_applicable' by migration 124_reflex_action_
	// taxonomy.sql normally; a CHECK constraint on combining_algorithm
	// (only IN ('deny_overrides','first_applicable','all_applicable') is
	// ever a valid row) rules out simulating this via an empty-string
	// row at the real-schema level, so a removed row is the realistic
	// trigger here — the empty-string variant is covered instead at the
	// Resolve() primitive level (resolve_test.go's
	// TestResolve_EmptyCombiningAlgorithm_LogsWarning), where any
	// ActionKindLookup a caller supplies is fair game.
	//
	// The two probe rows above reference this kind via a real FK, so a
	// plain DELETE is rejected — foreign_keys is toggled off for this one
	// statement (single-connection pool, restored immediately after) to
	// simulate the row genuinely being gone, e.g. a partially-applied
	// migration or a direct edit outside any FK-checked path, matching
	// engine.go's own doc comment on what actionKinds cache staleness
	// covers.
	if _, err := st.DB.ExecContext(context.Background(), `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatalf("disable foreign_keys: %v", err)
	}
	if _, err := st.DB.ExecContext(context.Background(),
		`DELETE FROM reflex_action_kinds WHERE name = ?`,
		store.ReflexActionDispatchToAgent,
	); err != nil {
		t.Fatalf("delete reflex_action_kinds row: %v", err)
	}
	if _, err := st.DB.ExecContext(context.Background(), `PRAGMA foreign_keys = ON`); err != nil {
		t.Fatalf("re-enable foreign_keys: %v", err)
	}

	tools := &recordingReflexDispatchToolService{}
	s := &chatServiceImpl{
		store:        st,
		reflexEngine: reflexes.NewEngine(st, nil),
		tools:        tools,
	}

	const userMessage = "probe-multi-candidate-token please route this"
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	classifyAndAttach(ls, "sess-multi-candidate-1", userMessage, nil)

	var buf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	ch := make(chan chat.StreamEvent, 4)
	out := s.attemptReflexDispatch(
		context.Background(),
		"sess-multi-candidate-1", "turn-1", userMessage,
		"agent-multi-candidate-1", "advisor",
		ls, ch,
	)
	close(ch)

	if !out.Matched {
		t.Fatalf("Matched = false, want true (both dispatch_to_agent probes should have fired and been selected under the degraded all_applicable fail-open)")
	}
	// The fail-open target itself is unchanged: a missing kind row still
	// degrades to all_applicable, not an error and not a change to
	// first_applicable's real seeded intent.
	if out.AgentSlug != "planner" {
		t.Errorf("AgentSlug = %q, want planner (first candidate's declared target still used)", out.AgentSlug)
	}

	logged := buf.String()
	if !strings.Contains(logged, "level=WARN") {
		t.Fatalf("log output missing a Warn-level line; got:\n%s", logged)
	}
	if !strings.Contains(logged, "resolved multiple candidates") {
		t.Errorf("log output missing the multi-candidate canary message; got:\n%s", logged)
	}
	if !strings.Contains(logged, "candidate_count=2") {
		t.Errorf("log output missing candidate_count=2; got:\n%s", logged)
	}
}
