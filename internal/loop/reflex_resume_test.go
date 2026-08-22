package loop

// TASKS/loops/11-loop-event-predicate-trigger.md -- the required
// end-to-end test (Done-means): a real LoopRun parked in
// waiting_on_escalation ("WAIT status" -- see this task's own Work Log
// for why that's the real, collapsed status literal, not a distinct
// "wait" one) with a real resume_loop_run reflex attached, its event
// trigger becoming true, and LoopEngine.Resume genuinely being called as
// a result via the real evaluation entry point
// (service.EvaluateLoopRunResumeReflexes) -- never by calling
// eng.Resume/eng.ResumeLoopRun directly from the test itself.
//
// Lives in this package (not internal/service, where
// EvaluateLoopRunResumeReflexes is actually declared) because it needs a
// real *LoopEngine as the service.LoopRunResumer -- internal/loop already
// imports internal/service (task 08), so this test file can freely call
// service.EvaluateLoopRunResumeReflexes and pass a real *LoopEngine value
// into it; internal/service's own tests cannot do the reverse (that's the
// entire reason LoopRunResumer is an interface in the first place -- see
// internal/loop/reflex_resume.go's package doc comment).

import (
	"context"
	"log/slog"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestResumeLoopRunReflex_FiresLoopEngineResume_ViaRealEvaluationCadence(t *testing.T) {
	ctx := context.Background()
	exec := &fakeStepExecutor{}
	st, registry, eng := newLoopEngineTestFixtures(t, exec)
	profile := createTestLoopAgentProfile(t, st, "loop-agent-resume-reflex")

	goal := store.Goal{Intent: "a target only confirmable by an external check"}
	if err := goal.SetAcceptanceCriteria([]string{"tests pass"}); err != nil {
		t.Fatalf("SetAcceptanceCriteria: %v", err)
	}
	if err := st.CreateGoal(ctx, &goal); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	// Iteration 3 (the one Resume launches) is what actually produces the
	// qualifying goal evidence -- mirrors engine_test.go's own
	// TestLoopEngine_Run_MultiIterationLoop_CompletesOnGoalMet pattern, so
	// this test can assert real forward progress (waiting_on_escalation ->
	// completed) as the visible proof that Resume genuinely ran a further
	// iteration, not just that some function returned true.
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

	wf := oneStepIterationDefinition("test-resume-loop-run-reflex")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Budget{MaxIterations: 2} exhausts on iteration 2 (no goal evidence
	// recorded yet -- callCount is 1 then 2, never 3, during Run) and
	// escalates -- the same real path
	// TestLoopEngine_Run_BudgetExhausted_EscalatesThenResumeContinues
	// already proves reaches store.LoopRunStatusWaitingOnEscalation.
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
	loopRunID := result.LoopRunID

	// Attach a real resume_loop_run reflex to this specific LoopRun --
	// task 08/10's own job in production (see migration 142's doc
	// comment); this test creates the row directly via InsertAgentReflex,
	// the same way every other reflex-fixture test in this codebase seeds
	// a candidate row, since authoring the row itself is out of this
	// task's "What to do" scope.
	reflexID, err := st.InsertAgentReflex(ctx, store.AgentReflex{
		Name:        "resume-" + loopRunID,
		ClassTag:    "process",
		TriggerKind: store.ReflexTriggerEvent,
		TriggerSpec: `{"name":"external_check_passed"}`,
		ActionKind:  store.ReflexActionResumeLoopRun,
		ActionSpec:  `{"loop_run_id":"` + loopRunID + `"}`,
		CreatedBy:   "system",
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex(resume_loop_run): %v", err)
	}

	reflexEngine := reflexes.NewEngine(st, slog.Default())

	// Step 1: the external condition has NOT happened yet -- the real
	// trigger-spec AST (EvaluateTrigger, unchanged) must evaluate false,
	// and LoopEngine.Resume must genuinely NOT be called as a result. The
	// only way to observe this from outside is that the LoopRun's own
	// persisted state does not move.
	fired, err := service.EvaluateLoopRunResumeReflexes(ctx, reflexEngine, eng, loopRunID, reflexes.State{})
	if err != nil {
		t.Fatalf("EvaluateLoopRunResumeReflexes (condition not yet true): %v", err)
	}
	if fired {
		t.Fatal("fired = true before the external condition occurred, want false")
	}
	stillWaiting, err := st.GetLoopRun(ctx, loopRunID)
	if err != nil {
		t.Fatalf("GetLoopRun: %v", err)
	}
	if stillWaiting.Status != store.LoopRunStatusWaitingOnEscalation || stillWaiting.CurrentIteration != 2 {
		t.Fatalf("loop run advanced despite the trigger not firing: status=%q current_iteration=%d", stillWaiting.Status, stillWaiting.CurrentIteration)
	}

	// Step 2: the external condition becomes true. This call is the "real
	// evaluation cadence" this task's own item 3 investigation identified
	// (a scheduled tick's own eventual call, in production) -- the test
	// never calls eng.Resume or eng.ResumeLoopRun directly.
	fired, err = service.EvaluateLoopRunResumeReflexes(ctx, reflexEngine, eng, loopRunID, reflexes.State{
		Events: []reflexes.EventSignal{{EventType: "external_check_passed", Category: "test"}},
	})
	if err != nil {
		t.Fatalf("EvaluateLoopRunResumeReflexes (condition true): %v", err)
	}
	if !fired {
		t.Fatal("fired = false once the external condition occurred, want true")
	}

	// Prove LoopEngine.Resume genuinely ran a further iteration: the
	// LoopRun independently re-fetched from the store must have advanced
	// past waiting_on_escalation to completed (iteration 3's own
	// evidence-recording llmFunc branch only runs on a real Resume-driven
	// third iteration).
	resumed, err := st.GetLoopRun(ctx, loopRunID)
	if err != nil {
		t.Fatalf("GetLoopRun (after resume): %v", err)
	}
	if resumed.Status != store.LoopRunStatusCompleted {
		t.Fatalf("loop run status = %q, want completed (LoopEngine.Resume must have genuinely run)", resumed.Status)
	}
	if resumed.CurrentIteration != 3 {
		t.Fatalf("loop run current_iteration = %d, want 3", resumed.CurrentIteration)
	}
	if callCount != 3 {
		t.Fatalf("llmFunc callCount = %d, want 3 (iterations 1, 2, and the resume-driven 3rd)", callCount)
	}

	// And the reflex's own telemetry (fired_count) reflects the real fire.
	reflexRow, err := st.GetAgentReflex(ctx, reflexID)
	if err != nil {
		t.Fatalf("GetAgentReflex: %v", err)
	}
	if reflexRow.FiredCount != 1 {
		t.Errorf("reflexRow.FiredCount = %d, want 1", reflexRow.FiredCount)
	}
}
