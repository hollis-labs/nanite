package service

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/store"
)

type fakeLoopRunResumer struct{ calls []string }

func (f *fakeLoopRunResumer) ResumeLoopRun(_ context.Context, id string) error {
	f.calls = append(f.calls, id)
	return nil
}

func TestEvaluateLoopRunResumeReflexesRetainedRuleCannotResume(t *testing.T) {
	st := newTestStore(t)
	ctx := t.Context()
	goal := store.Goal{Intent: "retained external condition"}
	if err := st.CreateGoal(ctx, &goal); err != nil {
		t.Fatal(err)
	}
	lr := &store.LoopRun{GoalID: goal.ID, DefinitionName: "retained-loop"}
	if err := lr.SetBudget(store.Budget{}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateLoopRun(ctx, lr); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateLoopRunStatus(ctx, lr.ID, store.LoopRunStatusWaitingOnEscalation, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(ctx, `INSERT INTO agent_reflexes(id,name,class_tag,trigger_kind,trigger_spec,action_kind,action_spec,created_by) VALUES('retained-reflex','retained','process','event','{"name":"ready"}','resume_loop_run',?,'system')`, `{"loop_run_id":"`+lr.ID+`"}`); err != nil {
		t.Fatal(err)
	}
	const query = `SELECT * FROM agent_reflexes ORDER BY id`
	before := immutableConfigSnapshot(t, st, query)
	resumer := &fakeLoopRunResumer{}
	fired, candidates, err := EvaluateLoopRunResumeReflexes(ctx, reflexes.NewEngine(st, nil), resumer, lr.ID, reflexes.State{Events: []reflexes.EventSignal{{EventType: "ready"}}})
	if !errors.Is(err, store.ErrImmutableAgentProfile) || fired || candidates || len(resumer.calls) != 0 {
		t.Fatalf("fired=%v candidates=%v calls=%v err=%v", fired, candidates, resumer.calls, err)
	}
	immutableConfigUnchanged(t, st, query, before)
	got, err := st.GetLoopRun(ctx, lr.ID)
	if err != nil || got.Status != store.LoopRunStatusWaitingOnEscalation {
		t.Fatalf("retained loop: %+v %v", got, err)
	}
	// Even a nonexistent run or absent store must refuse before retained reads.
	_, _, err = EvaluateLoopRunResumeReflexes(ctx, nil, resumer, "missing", reflexes.State{})
	if !errors.Is(err, store.ErrImmutableAgentProfile) {
		t.Fatal(err)
	}
}
