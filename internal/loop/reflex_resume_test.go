package loop

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestResumeLoopRunReflex_RetainedRuleCannotResume(t *testing.T) {
	st, eng, exec, loopRunID := retainedWaitingLoop(t)
	seedRetainedResumeRule(t, st, loopRunID, store.ReflexTriggerEvent, `{"name":"external_check_passed"}`)
	before := retainedLoopResumeState(t, st)
	engine := reflexes.NewEngine(st, nil)
	for _, state := range []reflexes.State{
		{},
		{Events: []reflexes.EventSignal{{EventType: "external_check_passed", Category: "test"}}},
	} {
		fired, candidates, err := service.EvaluateLoopRunResumeReflexes(t.Context(), engine, eng, loopRunID, state)
		if !errors.Is(err, store.ErrImmutableAgentProfile) || fired || candidates {
			t.Fatalf("retained evaluation: fired=%v candidates=%v err=%v", fired, candidates, err)
		}
		if exec.calls != 0 {
			t.Fatalf("refused evaluation executed %d steps", exec.calls)
		}
		assertRetainedLoopResumeState(t, st, before)
	}
}

type refusedLoopResumer struct{ calls int }

func (r *refusedLoopResumer) ResumeLoopRun(context.Context, string) error {
	r.calls++
	return nil
}

func TestResumeLoopRunReflex_RefusesBeforeDependencies(t *testing.T) {
	resumer := &refusedLoopResumer{}
	for _, runID := range []string{"", "missing-retained-loop"} {
		fired, candidates, err := service.EvaluateLoopRunResumeReflexes(t.Context(), nil, resumer, runID, reflexes.State{})
		if !errors.Is(err, store.ErrImmutableAgentProfile) || fired || candidates || resumer.calls != 0 {
			t.Fatalf("missing dependencies: fired=%v candidates=%v calls=%d err=%v", fired, candidates, resumer.calls, err)
		}
	}
}

func TestResumeLoopRunReflex_EventTriggerRemainsPure(t *testing.T) {
	for _, test := range []struct {
		name  string
		state reflexes.State
		want  bool
	}{
		{"absent", reflexes.State{}, false},
		{"other", reflexes.State{Events: []reflexes.EventSignal{{EventType: "other"}}}, false},
		{"matched", reflexes.State{Events: []reflexes.EventSignal{{EventType: "external_check_passed"}}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := reflexes.EvaluateTrigger(store.ReflexTriggerEvent, `{"name":"external_check_passed"}`, test.state)
			if err != nil || got != test.want {
				t.Fatalf("pure event trigger=%v,%v want %v", got, err, test.want)
			}
		})
	}
}

// Retained journal rows are constructed in a private DB without launching an
// actor or enrolling a mutable rule. The real engine remains wired so an
// accidental resume can change rows or reach the step executor.
func retainedWaitingLoop(t *testing.T) (*store.Store, *LoopEngine, *fakeStepExecutor, string) {
	t.Helper()
	exec := &fakeStepExecutor{}
	st, registry, eng := newLoopEngineTestFixtures(t, exec)
	wf := oneStepIterationDefinition("retained-resume-loop")
	if err := registry.Register(wf); err != nil {
		t.Fatal(err)
	}
	goal := &store.Goal{Intent: "retained external check"}
	if err := goal.SetAcceptanceCriteria([]string{"tests pass"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateGoal(t.Context(), goal); err != nil {
		t.Fatal(err)
	}
	lr := &store.LoopRun{GoalID: goal.ID, DefinitionName: wf.Name, Status: store.LoopRunStatusWaitingOnEscalation, CurrentIteration: 2}
	if err := lr.SetBudget(store.Budget{MaxIterations: 2}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateLoopRun(t.Context(), lr); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		run := &store.WorkflowRunRow{ID: fmt.Sprintf("retained-iteration-%d", i), DefinitionName: wf.Name, Status: "completed", LoopRunID: &lr.ID, LoopIteration: &i}
		if err := st.CreateWorkflowRun(t.Context(), run); err != nil {
			t.Fatal(err)
		}
		step := &store.WorkflowRunStepRow{ID: fmt.Sprintf("retained-step-%d", i), WorkflowRunID: run.ID, StepID: "work", Kind: "llm", Status: "completed", Output: "retained output"}
		if err := st.UpsertWorkflowRunStep(t.Context(), step); err != nil {
			t.Fatal(err)
		}
		iteration := &store.LoopRunIteration{LoopRunID: lr.ID, IterationNumber: i, WorkflowRunID: run.ID, Decision: store.LoopRunIterationDecisionWait, ProgressState: store.LoopRunIterationProgressProgress}
		if err := st.CreateLoopRunIteration(t.Context(), iteration); err != nil {
			t.Fatal(err)
		}
	}
	return st, eng, exec, lr.ID
}

// Direct SQL seeds history only; retired Insert/GetAgentReflex operations must
// never be used as an authority or compatibility writer.
func seedRetainedResumeRule(t *testing.T, st *store.Store, loopRunID, kind, spec string) {
	t.Helper()
	_, err := st.DB.ExecContext(t.Context(), `INSERT INTO agent_reflexes(id,name,class_tag,trigger_kind,trigger_spec,action_kind,action_spec,created_by,fired_count) VALUES(?,?,?,?,?,?,?,?,?)`, "retained-resume-rule", "Retained resume", "process", kind, spec, store.ReflexActionResumeLoopRun, `{"loop_run_id":"`+loopRunID+`"}`, "system", 7)
	if err != nil {
		t.Fatal(err)
	}
}

func retainedLoopResumeState(t *testing.T, st *store.Store) map[string][][]any {
	t.Helper()
	tables, err := st.DB.QueryContext(t.Context(), `SELECT name FROM sqlite_master WHERE type='table' AND (name LIKE 'loop_%' OR name LIKE 'workflow_%' OR name LIKE 'goal%' OR name LIKE 'agent_%' OR name LIKE 'actor_%' OR name='session_actor_bindings' OR name='sessions') ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var name string
		if err := tables.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := tables.Err(); err != nil {
		t.Fatal(err)
	}
	if err := tables.Close(); err != nil {
		t.Fatal(err)
	}
	state := make(map[string][][]any, len(names))
	for _, name := range names {
		rows, err := st.DB.QueryContext(t.Context(), fmt.Sprintf(`SELECT * FROM %q ORDER BY rowid`, name))
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		state[name] = make([][]any, 0)
		for rows.Next() {
			values := make([]any, len(columns))
			targets := make([]any, len(columns))
			for i := range values {
				targets[i] = &values[i]
			}
			if err := rows.Scan(targets...); err != nil {
				t.Fatal(err)
			}
			for i, value := range values {
				if raw, ok := value.([]byte); ok {
					values[i] = string(raw)
				}
			}
			state[name] = append(state[name], values)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return state
}

func assertRetainedLoopResumeState(t *testing.T, st *store.Store, before map[string][][]any) {
	t.Helper()
	after := retainedLoopResumeState(t, st)
	for table, rows := range before {
		if !reflect.DeepEqual(rows, after[table]) {
			t.Fatalf("refused resume changed %s", table)
		}
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("refused resume changed journal or authority state")
	}
}
