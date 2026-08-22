package loop

// TASKS/loops/08-loop-engine-core.md's own regression coverage -- "Done
// means" requires (1) a real, multi-iteration Run against a trivial
// single-llm-step WorkflowDefinition ending in COMPLETE, with
// loop_run_iterations rows created/completed in order and
// loop_runs.current_iteration/no_progress_streak updating correctly, (2) a
// WAIT/ESCALATE decision persisting status and returning without blocking,
// with a subsequent Resume picking the loop back up correctly, and (3) the
// one-active-LoopRun-per-goal_id rule enforced and tested.
//
// Live-verification safety (EXECUTION-PROCESS.md): every test below uses a
// real, t.TempDir()-rooted SQLite *store.Store (newTestLoopStore, mirroring
// internal/service/workflow_engine_test.go's/team_run_launcher_test.go's
// own newTestWorkflowStore convention) and a real BuiltinWorkflowEngine +
// WorkflowLauncher -- only the leaf StepExecutor is a stub
// (fakeStepExecutor, already declared in this package's own
// decide_test.go), matching this task's own "Done means" instruction ("no
// real LLM call needed if the test uses a stub StepExecutor, matching
// whatever test-double convention workflow_engine_test.go/
// team_run_launcher_test.go already use").

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

func newTestLoopStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func newLoopEngineTestFixtures(t *testing.T, exec agentworkflow.StepExecutor) (*store.Store, *agentworkflow.Registry, *LoopEngine) {
	t.Helper()
	st := newTestLoopStore(t)
	registry := agentworkflow.NewRegistry(nil)
	builtin := service.NewBuiltinWorkflowEngine(st)
	engines := map[string]agentworkflow.WorkflowEngine{agentworkflow.EngineBuiltin: builtin}
	durable := service.NewDurableAgentService(st)
	launcher := service.NewWorkflowLauncher(registry, engines, exec, durable)
	return st, registry, NewLoopEngine(st, registry, launcher)
}

func createTestLoopAgentProfile(t *testing.T, st *store.Store, slug string) *store.AgentProfile {
	t.Helper()
	p := &store.AgentProfile{Name: slug, Slug: slug, SystemPrompt: "you are " + slug}
	if err := st.CreateAgent(p); err != nil {
		t.Fatalf("CreateAgent(%s): %v", slug, err)
	}
	return p
}

// oneStepIterationDefinition is the "trivial single-llm-step
// WorkflowDefinition" this task's own "Done means" names -- no Verify
// modifier, so classifyIterationProgress's own "no verify configured, clean
// completion" branch classifies a successful run as PROGRESS.
func oneStepIterationDefinition(name string) agentworkflow.WorkflowDefinition {
	return agentworkflow.WorkflowDefinition{
		Name: name,
		Steps: []agentworkflow.StepDefinition{
			{
				ID:   "work",
				Kind: agentworkflow.StepKindLLM,
				Config: map[string]any{
					"provider": "anthropic",
					"prompt":   "do the thing",
					"agent_id": "agent-1",
				},
			},
		},
	}
}

// --- Done means #1: a real, multi-iteration loop ending in COMPLETE ---

func TestLoopEngine_Run_MultiIterationLoop_CompletesOnGoalMet(t *testing.T) {
	ctx := context.Background()
	exec := &fakeStepExecutor{}
	st, registry, eng := newLoopEngineTestFixtures(t, exec)
	profile := createTestLoopAgentProfile(t, st, "loop-agent-1")

	goal := store.Goal{Intent: "ship the feature"}
	if err := goal.SetAcceptanceCriteria([]string{"tests pass"}); err != nil {
		t.Fatalf("SetAcceptanceCriteria: %v", err)
	}
	if err := st.CreateGoal(ctx, &goal); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	// The third iteration's own step is what actually produces the
	// qualifying evidence -- a realistic shape (e.g. iteration 3's tool
	// step is what makes the tests pass), and proves Decide's goal-met
	// branch reacts to evidence recorded during the SAME iteration being
	// evaluated, not just evidence pre-existing before Run was ever
	// called.
	callCount := 0
	exec.llmFunc = func(req agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error) {
		callCount++
		if callCount == 3 {
			if err := st.RecordGoalEvidence(ctx, &store.GoalEvidence{
				GoalID: goal.ID, EvidenceType: store.GoalEvidenceTypeTestSuite,
				RefTable: "workflow_run_steps", RefID: req.StepID, Summary: "tests pass",
			}); err != nil {
				t.Fatalf("RecordGoalEvidence: %v", err)
			}
		}
		return agentworkflow.LLMStepResult{Text: "ok"}, nil
	}

	wf := oneStepIterationDefinition("test-iteration-complete")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}

	result, err := eng.Run(ctx, LoopDefinition{WorkflowName: wf.Name}, LoopInput{
		GoalID:         goal.ID,
		AgentProfileID: profile.ID,
		Budget:         store.Budget{MaxIterations: 10},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != store.LoopRunStatusCompleted {
		t.Fatalf("Status = %q, want completed (last decision %+v)", result.Status, result.LastDecision)
	}
	if result.CurrentIteration != 3 {
		t.Fatalf("CurrentIteration = %d, want 3", result.CurrentIteration)
	}
	if result.LastDecision.Kind != DecisionComplete {
		t.Fatalf("LastDecision.Kind = %q, want complete", result.LastDecision.Kind)
	}

	lr, err := st.GetLoopRun(ctx, result.LoopRunID)
	if err != nil {
		t.Fatalf("GetLoopRun: %v", err)
	}
	if lr.Status != store.LoopRunStatusCompleted {
		t.Fatalf("loop_run status = %q, want completed", lr.Status)
	}
	if lr.CurrentIteration != 3 {
		t.Fatalf("loop_run current_iteration = %d, want 3", lr.CurrentIteration)
	}
	if lr.NoProgressStreak != 0 {
		t.Fatalf("loop_run no_progress_streak = %d, want 0 (every iteration made progress or completed)", lr.NoProgressStreak)
	}
	if lr.CompletedAt == "" {
		t.Fatal("loop_run completed_at is empty, want set")
	}

	iterations, err := st.ListLoopRunIterations(ctx, result.LoopRunID)
	if err != nil {
		t.Fatalf("ListLoopRunIterations: %v", err)
	}
	if len(iterations) != 3 {
		t.Fatalf("len(iterations) = %d, want 3", len(iterations))
	}
	wantDecisions := []string{
		store.LoopRunIterationDecisionContinue,
		store.LoopRunIterationDecisionContinue,
		store.LoopRunIterationDecisionComplete,
	}
	for i, it := range iterations {
		if it.IterationNumber != i+1 {
			t.Fatalf("iterations[%d].IterationNumber = %d, want %d", i, it.IterationNumber, i+1)
		}
		if it.WorkflowRunID == "" {
			t.Fatalf("iterations[%d] has no workflow_run_id", i)
		}
		if it.CompletedAt == "" {
			t.Fatalf("iterations[%d] has no completed_at", i)
		}
		if it.Decision != wantDecisions[i] {
			t.Fatalf("iterations[%d].Decision = %q, want %q", i, it.Decision, wantDecisions[i])
		}

		run, err := st.GetWorkflowRun(it.WorkflowRunID)
		if err != nil {
			t.Fatalf("GetWorkflowRun(%s): %v", it.WorkflowRunID, err)
		}
		if run.LoopRunID == nil || *run.LoopRunID != result.LoopRunID {
			t.Fatalf("workflow_run %s LoopRunID = %v, want %s", it.WorkflowRunID, run.LoopRunID, result.LoopRunID)
		}
		if run.LoopIteration == nil || *run.LoopIteration != i+1 {
			t.Fatalf("workflow_run %s LoopIteration = %v, want %d", it.WorkflowRunID, run.LoopIteration, i+1)
		}
	}
	if iterations[2].ProgressState != store.LoopRunIterationProgressGoalMet {
		t.Fatalf("final iteration progress_state = %q, want goal_met", iterations[2].ProgressState)
	}
	if iterations[0].ProgressState != store.LoopRunIterationProgressProgress {
		t.Fatalf("first iteration progress_state = %q, want progress", iterations[0].ProgressState)
	}
}

// --- Done means #2: WAIT/ESCALATE persists status and returns without
// blocking, and a subsequent Resume picks the loop back up correctly ---

func TestLoopEngine_Run_BudgetExhausted_EscalatesThenResumeContinues(t *testing.T) {
	ctx := context.Background()
	exec := &fakeStepExecutor{}
	st, registry, eng := newLoopEngineTestFixtures(t, exec)
	profile := createTestLoopAgentProfile(t, st, "loop-agent-2")

	goal := store.Goal{Intent: "an unreachable target"}
	if err := goal.SetAcceptanceCriteria([]string{"unreachable criterion"}); err != nil {
		t.Fatalf("SetAcceptanceCriteria: %v", err)
	}
	if err := st.CreateGoal(ctx, &goal); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	wf := oneStepIterationDefinition("test-iteration-escalate")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}

	result, err := eng.Run(ctx, LoopDefinition{WorkflowName: wf.Name}, LoopInput{
		GoalID:         goal.ID,
		AgentProfileID: profile.ID,
		Budget:         store.Budget{MaxIterations: 2, OnExhausted: store.LoopRunOnExhaustedEscalate},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != store.LoopRunStatusWaitingOnEscalation {
		t.Fatalf("Status = %q, want waiting_on_escalation", result.Status)
	}
	if result.CurrentIteration != 2 {
		t.Fatalf("CurrentIteration = %d, want 2", result.CurrentIteration)
	}
	if result.LastDecision.Kind != DecisionEscalate {
		t.Fatalf("LastDecision.Kind = %q, want escalate", result.LastDecision.Kind)
	}

	lr, err := st.GetLoopRun(ctx, result.LoopRunID)
	if err != nil {
		t.Fatalf("GetLoopRun: %v", err)
	}
	if lr.Status != store.LoopRunStatusWaitingOnEscalation {
		t.Fatalf("persisted loop_run status = %q, want waiting_on_escalation", lr.Status)
	}
	if lr.CompletedAt != "" {
		t.Fatalf("a paused (not terminal) loop_run must not have completed_at set, got %q", lr.CompletedAt)
	}

	// Resume: no input parameters (per this package's own fixed
	// interface) -- it must recover budget/policy/AgentProfileID purely
	// from persisted state (loopRunPersistentConfig, engine.go) and
	// launch one more real iteration, proving the recovery actually
	// works, even though it immediately re-escalates against the very
	// same persisted MaxIterations=2 budget.
	resumed, err := eng.Resume(ctx, result.LoopRunID)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resumed.Status != store.LoopRunStatusWaitingOnEscalation {
		t.Fatalf("resumed Status = %q, want waiting_on_escalation", resumed.Status)
	}
	if resumed.CurrentIteration != 3 {
		t.Fatalf("resumed CurrentIteration = %d, want 3 (Resume must launch one more real iteration)", resumed.CurrentIteration)
	}
	if resumed.LastDecision.Kind != DecisionEscalate {
		t.Fatalf("resumed LastDecision.Kind = %q, want escalate", resumed.LastDecision.Kind)
	}

	iterations, err := st.ListLoopRunIterations(ctx, result.LoopRunID)
	if err != nil {
		t.Fatalf("ListLoopRunIterations: %v", err)
	}
	if len(iterations) != 3 {
		t.Fatalf("len(iterations) = %d, want 3 after resume", len(iterations))
	}
	wantDecisions := []string{
		store.LoopRunIterationDecisionContinue,
		store.LoopRunIterationDecisionEscalate,
		store.LoopRunIterationDecisionEscalate,
	}
	for i, it := range iterations {
		if it.Decision != wantDecisions[i] {
			t.Fatalf("iterations[%d].Decision = %q, want %q", i, it.Decision, wantDecisions[i])
		}
		if it.WorkflowRunID == "" {
			t.Fatalf("iterations[%d] has no workflow_run_id", i)
		}
	}

	// A second Resume against the still-waiting LoopRun must behave
	// identically (idempotent re-entry, not a crash or a skipped
	// iteration) -- exercises Resume being called more than once against
	// the same pause, as task 10/11/12's own real callers might.
	resumedAgain, err := eng.Resume(ctx, result.LoopRunID)
	if err != nil {
		t.Fatalf("second Resume: %v", err)
	}
	if resumedAgain.CurrentIteration != 4 {
		t.Fatalf("second resumed CurrentIteration = %d, want 4", resumedAgain.CurrentIteration)
	}
}

// --- Done means #3: one-active-LoopRun-per-goal_id is enforced ---

func TestLoopEngine_Run_RejectsSecondActiveLoopRunForSameGoal(t *testing.T) {
	ctx := context.Background()
	exec := &fakeStepExecutor{}
	st, registry, eng := newLoopEngineTestFixtures(t, exec)
	profile := createTestLoopAgentProfile(t, st, "loop-agent-3")

	goal := store.Goal{Intent: "already has a running loop"}
	if err := st.CreateGoal(ctx, &goal); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	existing := &store.LoopRun{GoalID: goal.ID, DefinitionName: "whatever", Status: store.LoopRunStatusRunning}
	if err := existing.SetBudget(store.Budget{}); err != nil {
		t.Fatalf("SetBudget: %v", err)
	}
	if err := st.CreateLoopRun(ctx, existing); err != nil {
		t.Fatalf("CreateLoopRun: %v", err)
	}

	wf := oneStepIterationDefinition("test-iteration-rejected")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, err := eng.Run(ctx, LoopDefinition{WorkflowName: wf.Name}, LoopInput{
		GoalID:         goal.ID,
		AgentProfileID: profile.ID,
		Budget:         store.Budget{MaxIterations: 5},
	})
	if err == nil {
		t.Fatal("expected Run to reject a second active LoopRun for the same goal, got nil error")
	}
	if !errors.Is(err, ErrLoopRunAlreadyActive) {
		t.Fatalf("expected ErrLoopRunAlreadyActive, got %v", err)
	}
	if exec.calls != 0 {
		t.Fatalf("expected zero LLM calls -- Run must reject before launching anything, got %d", exec.calls)
	}

	// A waiting_on_escalation LoopRun is also "active" -- LoopRunActiveStatuses
	// includes it, not just "running" -- and must reject the same way.
	if err := st.UpdateLoopRunStatus(ctx, existing.ID, store.LoopRunStatusWaitingOnEscalation, nil); err != nil {
		t.Fatalf("UpdateLoopRunStatus: %v", err)
	}
	_, err = eng.Run(ctx, LoopDefinition{WorkflowName: wf.Name}, LoopInput{
		GoalID:         goal.ID,
		AgentProfileID: profile.ID,
		Budget:         store.Budget{MaxIterations: 5},
	})
	if !errors.Is(err, ErrLoopRunAlreadyActive) {
		t.Fatalf("expected ErrLoopRunAlreadyActive for a waiting_on_escalation LoopRun too, got %v", err)
	}

	// A terminal (completed) LoopRun, by contrast, must NOT block a new
	// launch against the same goal.
	now := time.Now().UTC()
	if err := st.UpdateLoopRunStatus(ctx, existing.ID, store.LoopRunStatusCompleted, &now); err != nil {
		t.Fatalf("UpdateLoopRunStatus(completed): %v", err)
	}
	result, err := eng.Run(ctx, LoopDefinition{WorkflowName: wf.Name}, LoopInput{
		GoalID:         goal.ID,
		AgentProfileID: profile.ID,
		Budget:         store.Budget{MaxIterations: 1, OnExhausted: store.LoopRunOnExhaustedFail},
	})
	if err != nil {
		t.Fatalf("expected Run to succeed once the prior loop_run is terminal, got %v", err)
	}
	if result.LoopRunID == existing.ID {
		t.Fatal("expected a brand-new loop_run, got the same id as the terminal one")
	}
}

// --- Bonus coverage: inline goal spec (LoopInput.Goal) ---

func TestLoopEngine_Run_InlineGoalSpec_CreatesGoalAndFailsOnBudgetExhausted(t *testing.T) {
	ctx := context.Background()
	exec := &fakeStepExecutor{}
	st, registry, eng := newLoopEngineTestFixtures(t, exec)
	profile := createTestLoopAgentProfile(t, st, "loop-agent-4")

	wf := oneStepIterationDefinition("test-iteration-inline-goal")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}

	result, err := eng.Run(ctx, LoopDefinition{WorkflowName: wf.Name}, LoopInput{
		Goal: &LoopGoalSpec{
			Intent:             "an inline goal",
			AcceptanceCriteria: []string{"never satisfied in this test"},
		},
		AgentProfileID: profile.ID,
		Budget:         store.Budget{MaxIterations: 1, OnExhausted: store.LoopRunOnExhaustedFail},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != store.LoopRunStatusFailed {
		t.Fatalf("Status = %q, want failed", result.Status)
	}

	lr, err := st.GetLoopRun(ctx, result.LoopRunID)
	if err != nil {
		t.Fatalf("GetLoopRun: %v", err)
	}
	goal, err := st.GetGoal(ctx, lr.GoalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if goal.Intent != "an inline goal" {
		t.Fatalf("goal.Intent = %q, want %q", goal.Intent, "an inline goal")
	}
}

func TestLoopEngine_Run_RejectsAmbiguousOrMissingGoalInput(t *testing.T) {
	ctx := context.Background()
	exec := &fakeStepExecutor{}
	st, registry, eng := newLoopEngineTestFixtures(t, exec)
	profile := createTestLoopAgentProfile(t, st, "loop-agent-5")

	goal := store.Goal{Intent: "x"}
	if err := st.CreateGoal(ctx, &goal); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	wf := oneStepIterationDefinition("test-iteration-goal-input-errors")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := eng.Run(ctx, LoopDefinition{WorkflowName: wf.Name}, LoopInput{
		AgentProfileID: profile.ID,
	}); err == nil {
		t.Fatal("expected an error when neither GoalID nor Goal is set")
	}

	if _, err := eng.Run(ctx, LoopDefinition{WorkflowName: wf.Name}, LoopInput{
		GoalID:         goal.ID,
		Goal:           &LoopGoalSpec{Intent: "also set"},
		AgentProfileID: profile.ID,
	}); err == nil {
		t.Fatal("expected an error when both GoalID and Goal are set")
	}
}
