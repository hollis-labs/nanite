package loop

import (
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/workflowhost"
)

func TestLoopRunMissingIssuerRefusesBeforeInlineGoalAndJournal(t *testing.T) {
	st := newTestLoopStore(t)
	actor := createTestLoopAgentProfile(t, st, "loop-refusal-prior")
	registry := agentworkflow.NewRegistry(nil)
	state, err := workflowhost.NewWorkflowStateStore(st)
	if err != nil {
		t.Fatal(err)
	}
	host, err := workflowhost.NewEngine(state)
	if err != nil {
		t.Fatal(err)
	}
	exec := &fakeStepExecutor{}
	// Actual production lifecycle has no issuer. A private existing actor does
	// not authorize creation of a new child instance, loop or inline goal.
	launcher := service.NewWorkflowLauncher(registry, host, exec, service.NewDurableAgentService(st))
	eng := NewLoopEngine(st, registry, launcher)
	queries := []string{"SELECT count(*) FROM goals", "SELECT count(*) FROM loop_runs", "SELECT count(*) FROM loop_run_iterations", "SELECT count(*) FROM workflow_runs", "SELECT count(*) FROM actor_instances", "SELECT count(*) FROM sessions"}
	before := make([]int, len(queries))
	for i, q := range queries {
		if operationErr := st.DB.QueryRowContext(t.Context(), q).Scan(&before[i]); operationErr != nil {
			t.Fatal(operationErr)
		}
	}
	result, err := eng.Run(t.Context(), LoopDefinition{WorkflowName: "private-definition"}, LoopInput{AgentProfileID: actor.ID, Goal: &LoopGoalSpec{Intent: "must not persist"}})
	if !errors.Is(err, store.ErrVerifiedActorRequired) || result.LoopRunID != "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for i, q := range queries {
		var after int
		if err := st.DB.QueryRowContext(t.Context(), q).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if after != before[i] {
			t.Fatalf("%s changed %d -> %d", q, before[i], after)
		}
	}
	if exec.calls != 0 {
		t.Fatalf("executor calls=%d", exec.calls)
	}
}
