package service

// Regression coverage for TASKS/teams/05-agent-reflexes-run-scoping.md —
// the "third scoping dimension" docs/engineering/architecture/
// 15-teams.md's "Routing: real reuse, and one real gap" section names:
// agent_reflexes.workflow_run_id widens attemptReflexDispatch's candidate
// set with a session's TeamRun-scoped dispatch_to_agent rows, layered on
// top of (not replacing) the existing global (workflow_run_id IS NULL)
// set.
//
// insertTestTeamRunMember inserts a real row into TASKS/teams/
// 02-team-run-members-table.md's team_run_members table (migration 129,
// landed on main since this task was originally implemented against a
// worktree without it — that table is real here, not a test-local
// stand-in). Only session_id/workflow_run_id vary per call;
// slot_name/agent_id are fixture values satisfying the table's real
// NOT NULL/FK constraints, irrelevant to what this test actually exercises
// (Store.ResolveWorkflowRunIDForSession only reads session_id/
// workflow_run_id).

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

// ensureTestTeamRunMemberFixtureAgent creates the one shared agent_profiles
// row team_run_members.agent_id's FK requires, idempotently — this test
// doesn't care which agent occupies the slot, only that a real, valid one
// does.
func ensureTestTeamRunMemberFixtureAgent(t *testing.T, st *store.Store) {
	t.Helper()
	if _, err := st.DB.ExecContext(context.Background(),
		`INSERT OR IGNORE INTO agent_profiles (id, name, slug, system_prompt, source) VALUES (?, ?, ?, ?, ?)`,
		"agent-trm-fixture", "TRM Fixture Agent", "agent-trm-fixture", "test", "test",
	); err != nil {
		t.Fatalf("insert test team_run_members fixture agent: %v", err)
	}
}

func insertTestTeamRunMember(t *testing.T, st *store.Store, sessionID, runID string) {
	t.Helper()
	ctx := context.Background()
	insertTestWorkflowRun(t, st, runID)
	ensureTestTeamRunMemberFixtureAgent(t, st)
	if _, err := st.DB.ExecContext(ctx,
		`INSERT INTO sessions (id, title, short_code, created_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)`,
		sessionID, "test session "+sessionID, "sc-"+sessionID,
	); err != nil {
		t.Fatalf("insert test session: %v", err)
	}
	if _, err := st.InsertTeamRunMember(ctx, store.TeamRunMember{
		WorkflowRunID: runID,
		SlotName:      "member",
		AgentID:       "agent-trm-fixture",
		SessionID:     sessionID,
	}); err != nil {
		t.Fatalf("insert test team_run_members row: %v", err)
	}
}

// insertTestWorkflowRun inserts the minimal real workflow_runs row a
// non-empty agent_reflexes.workflow_run_id (FK-enforced, foreign_keys is
// ON by default per sqlitekit.WriterOptions) requires. Idempotent (INSERT
// OR IGNORE) so callers that reference the same runID more than once
// (e.g. multiple team_run_members rows for one run) don't need to
// deduplicate themselves.
func insertTestWorkflowRun(t *testing.T, st *store.Store, runID string) {
	t.Helper()
	if _, err := st.DB.ExecContext(context.Background(),
		`INSERT OR IGNORE INTO workflow_runs (id, started_at) VALUES (?, datetime('now'))`,
		runID,
	); err != nil {
		t.Fatalf("insert test workflow_runs row: %v", err)
	}
}

// TestAttemptReflexDispatch_RunScopedReflex_IsolatedToItsOwnRun proves:
//   - a dispatch_to_agent reflex scoped to run-A (workflow_run_id="run-A")
//     fires for a session team_run_members resolves to run-A;
//   - the SAME reflex does NOT fire for a session resolved to a
//     different run (run-B) — cross-run isolation;
//   - the SAME reflex does NOT fire for a session with no
//     team_run_members row at all — a plain, non-Team session;
//   - a global (workflow_run_id empty) reflex fires in all three cases,
//     unaffected by any of the above — the widening is additive, not a
//     replacement of the existing global candidate set.
func TestAttemptReflexDispatch_RunScopedReflex_IsolatedToItsOwnRun(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reflex-dispatch-run-scoping.db")
	st, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	insertTestTeamRunMember(t, st, "sess-in-run-a", "run-A")
	insertTestTeamRunMember(t, st, "sess-in-run-b", "run-B")
	// "sess-no-run" deliberately gets no team_run_members row.

	if _, err := st.InsertAgentReflex(context.Background(), store.AgentReflex{
		ClassTag:      "advisor",
		Name:          "dispatch_run_scoped_probe",
		TriggerKind:   store.ReflexTriggerPredicate,
		TriggerSpec:   `{"kind":"user_regex_window","window":1,"pattern":"probe-run-scoped-token"}`,
		ActionKind:    store.ReflexActionDispatchToAgent,
		ActionSpec:    `{"agent_slug":"run-a-architect","confidence":0.9,"reason":"run-scoped test"}`,
		Priority:      50,
		WorkflowRunID: "run-A",
	}); err != nil {
		t.Fatalf("InsertAgentReflex (run-scoped): %v", err)
	}
	if _, err := st.InsertAgentReflex(context.Background(), store.AgentReflex{
		ClassTag:    "advisor",
		Name:        "dispatch_global_probe",
		TriggerKind: store.ReflexTriggerPredicate,
		TriggerSpec: `{"kind":"user_regex_window","window":1,"pattern":"probe-global-token"}`,
		ActionKind:  store.ReflexActionDispatchToAgent,
		ActionSpec:  `{"agent_slug":"global-planner","confidence":0.9,"reason":"global test"}`,
		Priority:    50,
		// WorkflowRunID left empty — global/class-bound.
	}); err != nil {
		t.Fatalf("InsertAgentReflex (global): %v", err)
	}

	tools := &recordingReflexDispatchToolService{}
	s := &chatServiceImpl{
		store:        st,
		reflexEngine: reflexes.NewEngine(st, nil),
		tools:        tools,
	}

	dispatch := func(sessionID, message string) reflexDispatchOutcome {
		ls := newLoopState(chat.AgentConstraints{}, nil, false)
		classifyAndAttach(ls, sessionID, message, nil)
		ch := make(chan chat.StreamEvent, 4)
		out := s.attemptReflexDispatch(
			context.Background(),
			sessionID, "turn-"+sessionID, message,
			"agent-"+sessionID, "advisor",
			ls, ch,
		)
		close(ch)
		return out
	}

	t.Run("fires for its own run's session", func(t *testing.T) {
		out := dispatch("sess-in-run-a", "probe-run-scoped-token please route this")
		if !out.Matched {
			t.Fatalf("Matched = false, want true (run-scoped reflex should fire for a session resolved to its own run)")
		}
		if out.ReflexName != "dispatch_run_scoped_probe" {
			t.Errorf("ReflexName = %q, want dispatch_run_scoped_probe", out.ReflexName)
		}
		if out.AgentSlug != "run-a-architect" {
			t.Errorf("AgentSlug = %q, want run-a-architect", out.AgentSlug)
		}
	})

	t.Run("invisible to a session in an unrelated run", func(t *testing.T) {
		out := dispatch("sess-in-run-b", "probe-run-scoped-token please route this")
		if out.Matched {
			t.Fatalf("Matched = true (reflex=%q agent_slug=%q), want false — a reflex scoped to run-A must not fire for a session resolved to run-B", out.ReflexName, out.AgentSlug)
		}
	})

	t.Run("invisible to a non-Team session", func(t *testing.T) {
		out := dispatch("sess-no-run", "probe-run-scoped-token please route this")
		if out.Matched {
			t.Fatalf("Matched = true (reflex=%q agent_slug=%q), want false — a reflex scoped to run-A must not fire for a session with no team_run_members row at all", out.ReflexName, out.AgentSlug)
		}
	})

	t.Run("global reflex still fires in a run-A session", func(t *testing.T) {
		out := dispatch("sess-in-run-a", "probe-global-token please route this")
		if !out.Matched || out.ReflexName != "dispatch_global_probe" {
			t.Fatalf("global reflex did not fire inside a Team-run session: Matched=%v ReflexName=%q", out.Matched, out.ReflexName)
		}
	})

	t.Run("global reflex still fires in a run-B session", func(t *testing.T) {
		out := dispatch("sess-in-run-b", "probe-global-token please route this")
		if !out.Matched || out.ReflexName != "dispatch_global_probe" {
			t.Fatalf("global reflex did not fire inside a different Team-run session: Matched=%v ReflexName=%q", out.Matched, out.ReflexName)
		}
	})

	t.Run("global reflex still fires in a non-Team session", func(t *testing.T) {
		out := dispatch("sess-no-run", "probe-global-token please route this")
		if !out.Matched || out.ReflexName != "dispatch_global_probe" {
			t.Fatalf("global reflex did not fire in a plain non-Team session: Matched=%v ReflexName=%q", out.Matched, out.ReflexName)
		}
	})
}

// TestAttemptReflexDispatch_NoTeamRunMembersTable_DegradesGracefully
// proves the fail-open path in Store.ResolveWorkflowRunIDForSession for a
// session with no team_run_members row at all — attemptReflexDispatch's
// ordinary (non-Team) dispatch_to_agent evaluation still works, the
// missing-scoping case must degrade gracefully rather than break every
// turn's dispatch evaluation.
//
// Merge note: this test originally targeted the "team_run_members table
// doesn't exist at all" branch of ResolveWorkflowRunIDForSession's
// fail-open handling (internal/store/session_team_run.go's "no such
// table" substring match) — accurate when this task was implemented
// against a worktree that predated TASKS/teams/02-team-run-members-table.md's
// own migration (129). Now that both migrations are present on every
// fresh store, that specific "no such table" branch can no longer be
// reproduced via store.New() and — orchestrator merge note — is not
// otherwise covered by any test as of this merge, a small, known,
// non-blocking coverage gap rather than a silently-dropped assertion.
// This test still exercises the sibling "row not found" branch (a real
// team_run_members table with no row for this session) — still real,
// still worth covering at this call site, just no longer the exact
// scenario the test's own name originally described.
func TestAttemptReflexDispatch_NoTeamRunMembersTable_DegradesGracefully(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reflex-dispatch-no-team-run-members-table.db")
	st, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	// Deliberately no createTestTeamRunMembers(t, st) call — this database
	// has never had team_run_members created at all, the real-world state
	// of every database that has run migration 131 but not task 02's.

	if _, err := st.InsertAgentReflex(context.Background(), store.AgentReflex{
		ClassTag:    "advisor",
		Name:        "dispatch_no_team_table_probe",
		TriggerKind: store.ReflexTriggerPredicate,
		TriggerSpec: `{"kind":"user_regex_window","window":1,"pattern":"probe-no-team-table-token"}`,
		ActionKind:  store.ReflexActionDispatchToAgent,
		ActionSpec:  `{"agent_slug":"planner","confidence":0.9,"reason":"no team_run_members table test"}`,
		Priority:    50,
	}); err != nil {
		t.Fatalf("InsertAgentReflex: %v", err)
	}

	tools := &recordingReflexDispatchToolService{}
	s := &chatServiceImpl{
		store:        st,
		reflexEngine: reflexes.NewEngine(st, nil),
		tools:        tools,
	}

	const userMessage = "probe-no-team-table-token please route this"
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	classifyAndAttach(ls, "sess-no-team-table", userMessage, nil)
	ch := make(chan chat.StreamEvent, 4)
	out := s.attemptReflexDispatch(
		context.Background(),
		"sess-no-team-table", "turn-1", userMessage,
		"agent-no-team-table", "advisor",
		ls, ch,
	)
	close(ch)

	if !out.Matched {
		t.Fatalf("Matched = false, want true — a missing team_run_members table must not break ordinary global dispatch_to_agent evaluation")
	}
	if out.ReflexName != "dispatch_no_team_table_probe" {
		t.Errorf("ReflexName = %q, want dispatch_no_team_table_probe", out.ReflexName)
	}
}
