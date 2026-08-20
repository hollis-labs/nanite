package selftools

// Regression coverage for TASKS/teams/05-agent-reflexes-run-scoping.md —
// matchDispatchToAgentReflex's own copy of the run-scoping widening
// internal/service/chat_reflex_dispatch.go's attemptReflexDispatch
// applies upstream (see that call site's own equivalent test,
// chat_reflex_dispatch_run_scoping_test.go, for the full rationale this
// file mirrors at its own, deliberately-independent call site — see
// self_tools_dispatch.go's own header comment for why both evaluations
// run rather than one calling the other).
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
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func insertTestWorkflowRun(t *testing.T, s *store.Store, runID string) {
	t.Helper()
	if _, err := s.DB.ExecContext(context.Background(),
		`INSERT OR IGNORE INTO workflow_runs (id, started_at) VALUES (?, datetime('now'))`,
		runID,
	); err != nil {
		t.Fatalf("insert test workflow_runs row: %v", err)
	}
}

// ensureTestTeamRunMemberFixtureAgent creates the one shared agent_profiles
// row team_run_members.agent_id's FK requires, idempotently — this test
// doesn't care which agent occupies the slot, only that a real, valid one
// does.
func ensureTestTeamRunMemberFixtureAgent(t *testing.T, s *store.Store) {
	t.Helper()
	if _, err := s.DB.ExecContext(context.Background(),
		`INSERT OR IGNORE INTO agent_profiles (id, name, slug, system_prompt, source) VALUES (?, ?, ?, ?, ?)`,
		"agent-trm-fixture", "TRM Fixture Agent", "agent-trm-fixture", "test", "test",
	); err != nil {
		t.Fatalf("insert test team_run_members fixture agent: %v", err)
	}
}

func insertTestTeamRunMember(t *testing.T, s *store.Store, sessionID, runID string) {
	t.Helper()
	ctx := context.Background()
	insertTestWorkflowRun(t, s, runID)
	ensureTestTeamRunMemberFixtureAgent(t, s)
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO sessions (id, title, short_code, created_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)`,
		sessionID, "test session "+sessionID, "sc-"+sessionID,
	); err != nil {
		t.Fatalf("insert test session: %v", err)
	}
	if _, err := s.InsertTeamRunMember(ctx, store.TeamRunMember{
		WorkflowRunID: runID,
		SlotName:      "member",
		AgentID:       "agent-trm-fixture",
		SessionID:     sessionID,
	}); err != nil {
		t.Fatalf("insert test team_run_members row: %v", err)
	}
}

// TestMatchDispatchToAgentReflex_RunScopedReflex_IsolatedToItsOwnRun
// mirrors chat_reflex_dispatch_run_scoping_test.go's
// TestAttemptReflexDispatch_RunScopedReflex_IsolatedToItsOwnRun exactly,
// at this package's own independent call site: a run-scoped
// dispatch_to_agent reflex fires only for a session team_run_members
// resolves to its own run, is invisible to a session resolved to a
// different run and to a plain non-Team session, and a global reflex
// keeps firing everywhere regardless.
func TestMatchDispatchToAgentReflex_RunScopedReflex_IsolatedToItsOwnRun(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if err := s.CreateAgent(&store.AgentProfile{
		ID:           "agent-run-scope-probe",
		Name:         "Agent Run Scope Probe",
		Slug:         "agent-run-scope-probe",
		Class:        "advisor",
		SystemPrompt: "test",
		Source:       "test",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	insertTestTeamRunMember(t, s, "sess-in-run-a", "run-A")
	insertTestTeamRunMember(t, s, "sess-in-run-b", "run-B")
	// "sess-no-run" deliberately gets no team_run_members row.

	if _, err := s.InsertAgentReflex(ctx, store.AgentReflex{
		AgentID:       "agent-run-scope-probe",
		Name:          "dispatch_run_scoped_probe_mcp",
		TriggerKind:   store.ReflexTriggerPredicate,
		TriggerSpec:   `{"kind":"user_regex_window","window":1,"pattern":"probe-run-scoped-mcp-token"}`,
		ActionKind:    store.ReflexActionDispatchToAgent,
		ActionSpec:    `{"agent_slug":"run-a-architect","confidence":0.9,"reason":"run-scoped test"}`,
		Priority:      50,
		WorkflowRunID: "run-A",
	}); err != nil {
		t.Fatalf("InsertAgentReflex (run-scoped): %v", err)
	}
	if _, err := s.InsertAgentReflex(ctx, store.AgentReflex{
		AgentID:     "agent-run-scope-probe",
		Name:        "dispatch_global_probe_mcp",
		TriggerKind: store.ReflexTriggerPredicate,
		TriggerSpec: `{"kind":"user_regex_window","window":1,"pattern":"probe-global-mcp-token"}`,
		ActionKind:  store.ReflexActionDispatchToAgent,
		ActionSpec:  `{"agent_slug":"global-planner","confidence":0.9,"reason":"global test"}`,
		Priority:    50,
		// WorkflowRunID left empty — global/agent-bound.
	}); err != nil {
		t.Fatalf("InsertAgentReflex (global): %v", err)
	}

	st := NewSelfToolsTransport(s)

	t.Run("fires for its own run's session", func(t *testing.T) {
		const msg = "probe-run-scoped-mcp-token please route this"
		hints := st.matchDispatchToAgentReflex(ctx, "sess-in-run-a", "turn-1", "agent-run-scope-probe", msg, msg)
		if hints == nil {
			t.Fatal("hints = nil, want a match (run-scoped reflex should fire for a session resolved to its own run)")
		}
		if hints.AgentSlug != "run-a-architect" {
			t.Errorf("AgentSlug = %q, want run-a-architect", hints.AgentSlug)
		}
	})

	t.Run("invisible to a session in an unrelated run", func(t *testing.T) {
		const msg = "probe-run-scoped-mcp-token please route this"
		hints := st.matchDispatchToAgentReflex(ctx, "sess-in-run-b", "turn-1", "agent-run-scope-probe", msg, msg)
		if hints != nil {
			t.Fatalf("hints = %+v, want nil — a reflex scoped to run-A must not fire for a session resolved to run-B", hints)
		}
	})

	t.Run("invisible to a non-Team session", func(t *testing.T) {
		const msg = "probe-run-scoped-mcp-token please route this"
		hints := st.matchDispatchToAgentReflex(ctx, "sess-no-run", "turn-1", "agent-run-scope-probe", msg, msg)
		if hints != nil {
			t.Fatalf("hints = %+v, want nil — a reflex scoped to run-A must not fire for a session with no team_run_members row at all", hints)
		}
	})

	t.Run("global reflex still fires in a run-A session", func(t *testing.T) {
		const msg = "probe-global-mcp-token please route this"
		hints := st.matchDispatchToAgentReflex(ctx, "sess-in-run-a", "turn-1", "agent-run-scope-probe", msg, msg)
		if hints == nil || hints.AgentSlug != "global-planner" {
			t.Fatalf("global reflex did not fire inside a Team-run session: hints=%+v", hints)
		}
	})

	t.Run("global reflex still fires in a run-B session", func(t *testing.T) {
		const msg = "probe-global-mcp-token please route this"
		hints := st.matchDispatchToAgentReflex(ctx, "sess-in-run-b", "turn-1", "agent-run-scope-probe", msg, msg)
		if hints == nil || hints.AgentSlug != "global-planner" {
			t.Fatalf("global reflex did not fire inside a different Team-run session: hints=%+v", hints)
		}
	})

	t.Run("global reflex still fires in a non-Team session", func(t *testing.T) {
		const msg = "probe-global-mcp-token please route this"
		hints := st.matchDispatchToAgentReflex(ctx, "sess-no-run", "turn-1", "agent-run-scope-probe", msg, msg)
		if hints == nil || hints.AgentSlug != "global-planner" {
			t.Fatalf("global reflex did not fire in a plain non-Team session: hints=%+v", hints)
		}
	})
}

// TestMatchDispatchToAgentReflex_NoTeamRunMembersTable_DegradesGracefully
// mirrors chat_reflex_dispatch_run_scoping_test.go's equivalent test at
// this package's own call site: for a session with no team_run_members
// row at all, the ordinary (non-Team) dispatch_to_agent evaluation still
// works — see that file's own merge note on why this no longer literally
// exercises a missing team_run_members table (both this task's migration,
// 131, and TASKS/teams/02-team-run-members-table.md's, 129, are now
// always present together on a fresh store).
func TestMatchDispatchToAgentReflex_NoTeamRunMembersTable_DegradesGracefully(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	// Deliberately no team_run_members row inserted for this session.

	if err := s.CreateAgent(&store.AgentProfile{
		ID:           "agent-no-team-table-mcp",
		Name:         "Agent No Team Table MCP",
		Slug:         "agent-no-team-table-mcp",
		Class:        "advisor",
		SystemPrompt: "test",
		Source:       "test",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	if _, err := s.InsertAgentReflex(ctx, store.AgentReflex{
		AgentID:     "agent-no-team-table-mcp",
		Name:        "dispatch_no_team_table_probe_mcp",
		TriggerKind: store.ReflexTriggerPredicate,
		TriggerSpec: `{"kind":"user_regex_window","window":1,"pattern":"probe-no-team-table-mcp-token"}`,
		ActionKind:  store.ReflexActionDispatchToAgent,
		ActionSpec:  `{"agent_slug":"planner","confidence":0.9,"reason":"no team_run_members table test"}`,
		Priority:    50,
	}); err != nil {
		t.Fatalf("InsertAgentReflex: %v", err)
	}

	st := NewSelfToolsTransport(s)

	const msg = "probe-no-team-table-mcp-token please route this"
	hints := st.matchDispatchToAgentReflex(ctx, "sess-no-team-table-mcp", "turn-1", "agent-no-team-table-mcp", msg, msg)
	if hints == nil {
		t.Fatal("hints = nil, want a match — a missing team_run_members table must not break ordinary global dispatch_to_agent evaluation")
	}
	if hints.AgentSlug != "planner" {
		t.Errorf("AgentSlug = %q, want planner", hints.AgentSlug)
	}
}
