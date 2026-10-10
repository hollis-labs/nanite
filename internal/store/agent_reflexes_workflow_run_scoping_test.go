package store

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestImmutableAgentReflexesRefuseGlobalAndRunScopedBehaviorWithoutChangingHistory(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	const agentID = "historical-scoped-reflex"
	retiredBehaviorHistoricalProfile(t, s, agentID)
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO workflow_runs(id,started_at) VALUES('historical-run','2026-09-01')`); err != nil {
		t.Fatal(err)
	}
	retiredBehaviorReflexFixture(t, s, AgentReflex{ID: "historical-global", AgentID: agentID, ClassTag: "advisor", Name: "global", CreatedBy: "operator", ProvenanceTier: "operator"})
	retiredBehaviorReflexFixture(t, s, AgentReflex{ID: "historical-scoped", AgentID: agentID, ClassTag: "advisor", Name: "scoped", CreatedBy: "operator", ProvenanceTier: "operator", WorkflowRunID: "historical-run"})
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO agent_reflex_opt_outs(agent_id,reflex_id,created_at) VALUES(?,'historical-scoped','2026-09-02')`, agentID); err != nil {
		t.Fatal(err)
	}
	queries := []string{`SELECT * FROM agent_reflexes ORDER BY id`, `SELECT * FROM agent_reflex_opt_outs ORDER BY agent_id,reflex_id`, `SELECT * FROM workflow_runs ORDER BY id`}
	before := make([][][]any, len(queries))
	for i, q := range queries {
		before[i] = retiredBehaviorSnapshot(t, s, q)
	}
	for _, id := range []string{"historical-global", "historical-scoped", "missing"} {
		row, err := s.GetAgentReflex(ctx, id)
		requireRetiredBehavior(t, err)
		if row != nil {
			t.Fatal("historical reflex exposed as current", row)
		}
		requireRetiredBehavior(t, s.UpdateAgentReflex(ctx, AgentReflex{ID: id, Status: ReflexStatusActive, Priority: 0}))
		requireRetiredBehavior(t, s.BumpAgentReflexFired(ctx, id, time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)))
		requireRetiredBehavior(t, s.DeleteAgentReflex(ctx, id))
		requireRetiredBehavior(t, s.SetAgentReflexOptOut(ctx, agentID, id))
		requireRetiredBehavior(t, s.ClearAgentReflexOptOut(ctx, agentID, id))
	}
	for _, runID := range []string{"historical-run", "unrelated", ""} {
		rows, err := s.ListAgentReflexesForWorkflowRun(ctx, runID, agentID, "advisor")
		requireRetiredBehavior(t, err)
		if len(rows) != 0 {
			t.Fatal("historical run-scoped behavior admitted", rows)
		}
	}
	readers := []func() ([]AgentReflex, error){
		func() ([]AgentReflex, error) { return s.ListAgentReflexesForAgent(ctx, agentID, "advisor") },
		func() ([]AgentReflex, error) { return s.ListAgentReflexesForAgent(ctx, "", "advisor") },
		func() ([]AgentReflex, error) { return s.ListAgentReflexesForLoopRun(ctx, "historical-run") },
		func() ([]AgentReflex, error) { return s.ListAllAgentReflexes(ctx, agentID) },
	}
	for _, read := range readers {
		rows, err := read()
		requireRetiredBehavior(t, err)
		if len(rows) != 0 {
			t.Fatal("historical behavior admitted", rows)
		}
	}
	optouts, err := s.ListAgentReflexOptOuts(ctx, agentID)
	requireRetiredBehavior(t, err)
	if len(optouts) != 0 {
		t.Fatal("historical opt-outs exposed as active authority")
	}
	for _, runID := range []string{"", "historical-run"} {
		row := AgentReflex{ID: "new-reflex", AgentID: agentID, Name: "new", TriggerKind: ReflexTriggerEvent, TriggerSpec: `{"name":"probe"}`, ActionKind: ReflexActionInjectReminder, ActionSpec: `{"body":"new"}`, WorkflowRunID: runID}
		id, insertErr := s.InsertAgentReflex(ctx, row)
		requireRetiredBehavior(t, insertErr)
		if id != "" {
			t.Fatal("refused insert returned identity", id)
		}
		inserted, absentErr := s.InsertAgentReflexIfAbsent(ctx, row)
		requireRetiredBehavior(t, absentErr)
		if inserted {
			t.Fatal("refused insert-if-absent reported insertion")
		}
	}
	for i, q := range queries {
		if after := retiredBehaviorSnapshot(t, s, q); !reflect.DeepEqual(before[i], after) {
			t.Fatalf("refused reflex operation changed retained history for %s: before=%v after=%v", q, before[i], after)
		}
	}
}

// The historical action vocabulary remains readable; it does not enroll an
// actor or authorize runtime use of a historical reflex.
func TestActionKindAllowsProvenanceTier_DispatchToAgent_OperatorTier(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	allowed, err := s.ActionKindAllowsProvenanceTier(ctx, ReflexActionDispatchToAgent, "operator")
	if err != nil {
		t.Fatalf("ActionKindAllowsProvenanceTier(dispatch_to_agent, operator): %v", err)
	}
	if !allowed {
		t.Fatalf("ActionKindAllowsProvenanceTier(dispatch_to_agent, operator) = false, want true")
	}

	deniedPlugin, err := s.ActionKindAllowsProvenanceTier(ctx, ReflexActionDispatchToAgent, "plugin")
	if err != nil {
		t.Fatalf("ActionKindAllowsProvenanceTier(dispatch_to_agent, plugin): %v", err)
	}
	if deniedPlugin {
		t.Fatalf("ActionKindAllowsProvenanceTier(dispatch_to_agent, plugin) = true, want false")
	}
}
