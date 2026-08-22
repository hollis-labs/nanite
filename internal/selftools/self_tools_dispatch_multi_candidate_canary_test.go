package selftools

// TASKS/reflex-taxonomy/08-fix-resolve-fail-open-visibility.md item 3: a
// canary log for the same kind-lookup-failure fail-open Resolve() itself
// now Warn-logs directly (internal/agent/reflexes/resolve.go) — when the
// dispatch_to_agent kind's combining_algorithm degrades to all_applicable
// (instead of its seeded first_applicable), more than one candidate can
// be selected in the same pass, but matchDispatchToAgentReflex only ever
// reads resolved.FiredReflexes[0]. This test proves that degraded case
// is itself operator-visible at this (second, independent —
// callExecuteTask's own downstream evaluation) call site too, mirroring
// internal/service/chat_reflex_dispatch_multi_candidate_canary_test.go's
// equivalent coverage of the upstream attemptReflexDispatch call site.

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestMatchDispatchToAgentReflex_KindLookupDegraded_LogsMultiCandidateCanary(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if err := s.CreateAgent(context.Background(), &store.AgentProfile{
		ID:           "agent-multi-candidate-probe",
		Name:         "Agent Multi Candidate Probe",
		Slug:         "agent-multi-candidate-probe",
		Class:        "advisor",
		SystemPrompt: "test",
		Source:       "test",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// Two real dispatch_to_agent rows, both matching the same
	// user_regex_window trigger — same pattern
	// self_tools_dispatch_cooldown_test.go's fixture uses.
	for i, name := range []string{"dispatch_multi_probe_a", "dispatch_multi_probe_b"} {
		if _, err := s.InsertAgentReflex(ctx, store.AgentReflex{
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
	// same "kind lookup fails" cause
	// internal/agent/reflexes/resolve_test.go's
	// TestResolve_KindLookupFailure_LogsWarning exercises directly
	// against Resolve(). A CHECK constraint on combining_algorithm rules
	// out simulating this via an empty-string row at the real-schema
	// level, so a removed row is the realistic trigger here — see
	// chat_reflex_dispatch_multi_candidate_canary_test.go's matching note
	// for the full accounting.
	//
	// The two probe rows above reference this kind via a real FK, so a
	// plain DELETE is rejected — foreign_keys is toggled off for this one
	// statement (single-connection pool, restored immediately after) to
	// simulate the row genuinely being gone.
	if _, err := s.DB.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatalf("disable foreign_keys: %v", err)
	}
	if _, err := s.DB.ExecContext(ctx,
		`DELETE FROM reflex_action_kinds WHERE name = ?`,
		store.ReflexActionDispatchToAgent,
	); err != nil {
		t.Fatalf("delete reflex_action_kinds row: %v", err)
	}
	if _, err := s.DB.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		t.Fatalf("re-enable foreign_keys: %v", err)
	}

	st := NewSelfToolsTransport(s)

	const msg = "probe-multi-candidate-token please route this"

	var buf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	hints := st.matchDispatchToAgentReflex(ctx, "sess-multi-candidate-1", "turn-1", "agent-multi-candidate-probe", msg, msg)
	if hints == nil {
		t.Fatal("hints = nil, want a match (both dispatch_to_agent probes should have fired and been selected under the degraded all_applicable fail-open)")
	}
	// The fail-open target itself is unchanged: a missing kind row still
	// degrades to all_applicable, not an error and not a change to
	// first_applicable's real seeded intent.
	if hints.AgentSlug != "planner" {
		t.Errorf("AgentSlug = %q, want planner (first candidate's declared target still used)", hints.AgentSlug)
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
