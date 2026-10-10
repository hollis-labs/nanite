package service

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestRetiredDispatchRunScopedAndGlobalRulesCannotSteerAnySession(t *testing.T) {
	st, actor, session := newRetiredDispatchFixture(t, "retired-run-scope")
	for _, runID := range []string{"retained-run-a", "retained-run-b"} {
		if err := st.CreateWorkflowRun(t.Context(), &store.WorkflowRunRow{ID: runID, DefinitionName: "private-run", Status: "running"}); err != nil {
			t.Fatal(err)
		}
	}
	sessions := []string{session.ID, "session-retired-run-b", "session-retired-no-run", "session-retired-old-team"}
	for _, sessionID := range sessions[1:] {
		if err := st.CreateSession(t.Context(), &store.Session{ID: sessionID, Title: "Private scope fixture"}); err != nil {
			t.Fatal(err)
		}
	}
	// Explicit previously authorized fresh memberships. No team issuer is used.
	for i, runID := range []string{"retained-run-a", "retained-run-b"} {
		if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO actor_team_run_members(workflow_run_id,slot_name,agent_id,session_id) VALUES(?,'prior-member',?,?)`, runID, actor.ID, sessions[i]); err != nil {
			t.Fatal(err)
		}
	}
	// Historical membership is retained independently and cannot select a run.
	if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO team_run_members(workflow_run_id,slot_name,agent_id,session_id) VALUES('retained-run-a','historical-member','historical-retired-run-scope',?)`, sessions[3]); err != nil {
		t.Fatal(err)
	}
	insertRetainedDispatchRule(t, st, store.AgentReflex{Name: "retained-run-rule", WorkflowRunID: "retained-run-a", Priority: 100})
	insertRetainedDispatchRule(t, st, store.AgentReflex{Name: "retained-global-rule", Priority: 10})
	insertRetainedDispatchRule(t, st, store.AgentReflex{Name: "retained-agent-rule", AgentID: "historical-retired-run-scope", Priority: 1000})
	for i, sessionID := range sessions {
		runID, found, err := st.ResolveWorkflowRunIDForSession(t.Context(), sessionID)
		if err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			want := []string{"retained-run-a", "retained-run-b"}[i]
			if !found || runID != want {
				t.Fatalf("prior membership: run=%q found=%v", runID, found)
			}
		} else if found || runID != "" {
			t.Fatalf("historical/no membership selected run: %q %v", runID, found)
		}
		assertRetainedDispatchInert(t, st, actor.ID, sessionID, "please route this", classifiedRetiredDispatchTurn(t, sessionID, "please route this"))
	}
}
