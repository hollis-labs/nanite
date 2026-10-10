package selftools

// Retained run membership and mutable routing rules remain historical and inert.
// Private fixtures use the preserved old tables; they never launch or enroll agents.

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
	// Private retained historical membership, never a team launch or enrollment.
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO team_run_members(id,workflow_run_id,slot_name,agent_id,session_id) VALUES(?,?,'member','agent-trm-fixture',?)`, "historical-member-"+sessionID, runID, sessionID); err != nil {
		t.Fatalf("insert retained historical team_run_members row: %v", err)
	}
}

func TestMatchDispatchToAgentReflex_RunScopedReflex_IsolatedToItsOwnRun(t *testing.T) {
	s := newTestStore(t)
	historical := retainedSelftoolsDispatchProfile(t, s, "retained-dispatch-run-scope")
	insertTestTeamRunMember(t, s, "sess-in-run-a", "run-A")
	insertTestTeamRunMember(t, s, "sess-in-run-b", "run-B")
	retainedSelftoolsDispatchRule(t, s, store.AgentReflex{Name: "retained-run-scoped", AgentID: historical.ID, WorkflowRunID: "run-A", Priority: 100, FiredCount: 7})
	retainedSelftoolsDispatchRule(t, s, store.AgentReflex{Name: "retained-global", Priority: 99, FiredCount: 3})
	before := selftoolsDispatchSnapshot(t, s)
	st := newTestSelfToolsTransport(s)
	for _, session := range []string{"sess-in-run-a", "sess-in-run-b", "sess-no-run"} {
		for _, message := range []string{"probe-run-scoped-mcp-token please route this", "probe-global-mcp-token please route this"} {
			if hints := st.matchDispatchToAgentReflex(t.Context(), session, historical.ID, message); hints != nil {
				t.Fatalf("historical run/global route selected for %q: %+v", session, hints)
			}
			requireSelftoolsDispatchUnchanged(t, s, before)
		}
	}
}

func TestMatchDispatchToAgentReflex_NoTeamRunMembersTable_DegradesGracefully(t *testing.T) {
	s := newTestStore(t)
	historical := retainedSelftoolsDispatchProfile(t, s, "retained-dispatch-no-run")
	retainedSelftoolsDispatchRule(t, s, store.AgentReflex{Name: "retained-no-team", AgentID: historical.ID, FiredCount: 5})
	before := selftoolsDispatchSnapshot(t, s)
	st := newTestSelfToolsTransport(s)
	for _, id := range []string{historical.ID, historical.Slug, "", "unknown-agent"} {
		if hints := st.matchDispatchToAgentReflex(t.Context(), "session-without-members", id, "probe-no-team-table-mcp-token please route this"); hints != nil {
			t.Fatalf("retained rule became fallback for %q: %+v", id, hints)
		}
	}
	requireSelftoolsDispatchUnchanged(t, s, before)
	// Matcher has no historical dependency, even when the private store is unavailable.
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if hints := st.matchDispatchToAgentReflex(t.Context(), "unavailable-store", historical.ID, "Implement a task"); hints != nil {
		t.Fatalf("unavailable history produced hints: %+v", hints)
	}
	if hints := (&SelfToolsTransport{}).matchDispatchToAgentReflex(t.Context(), "no-store", historical.ID, "Implement a task"); hints != nil {
		t.Fatalf("missing store produced hints: %+v", hints)
	}
}
