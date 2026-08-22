package loop

// Integration-fix end-to-end coverage for TickResumeBridge (tick_resume.go)
// -- TASKS/ESCALATIONS.md's 2026-08-21 entry ("Phase 3's two parallel
// trigger tasks (11, 12) built compatible but disconnected mechanisms").
// Proves the real gap that entry diagnosed is now closed: a scheduled
// loop_run_tick's dispatch target (RunnerAdapter.Loops, wired to
// TickResumeBridge in cmd/nanite/main.go) genuinely respects an attached
// resume_loop_run reflex's trigger-spec predicate instead of blind-resuming
// unconditionally.
//
// Setup mirrors reflex_resume_test.go's own
// TestResumeLoopRunReflex_FiresLoopEngineResume_ViaRealEvaluationCadence
// (this same package's task-11 end-to-end test): a Budget{MaxIterations: 2}
// escalates to waiting_on_escalation with zero goal evidence recorded, and
// a further, Resume-driven 3rd iteration is what actually records
// qualifying evidence -- so "the loop genuinely advanced" is directly
// observable as a real status/iteration transition on the re-fetched
// LoopRun row, not just a bool a function happened to return.
//
// The trigger-true/trigger-false split below uses a "scope_tier" predicate
// node (internal/agent/reflexes/evaluator.go's evalStringEquals) rather
// than an "event" trigger: TickResumeBridge.Resume builds a headless
// reflexes.State{} internally (see tick_resume.go's own doc comment for
// why -- no live chat session, no external event feed wired into this call
// site by this fix), so there is no way for a test to inject a
// State.Events signal through the bridge's own Resume(ctx, loopRunID)
// signature the way reflex_resume_test.go's direct
// service.EvaluateLoopRunResumeReflexes call could. A "scope_tier"
// predicate compared against "" genuinely, trivially matches an unset
// (also "") State.ScopeTier -- evalStringEquals's own doc comment
// documents this exact trivial-match behavior -- giving a real,
// unmodified-EvaluateTrigger-code way to author a trigger that
// deterministically evaluates true (or, with any non-empty value, false)
// against the bridge's real headless state, without needing to touch
// EvaluateTrigger or invent a test-only trigger kind.

import (
	"context"
	"log/slog"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
)

// runToWaitingOnEscalation drives a fresh LoopRun to
// store.LoopRunStatusWaitingOnEscalation at CurrentIteration=2 via a real
// Budget{MaxIterations: 2} exhaustion -- the same real path
// reflex_resume_test.go's own end-to-end test and
// tick_schedule_test.go's TestEvaluateDecideAndAct_DecisionWait_... rely on.
// evidenceOnThirdCall, if true, wires exec.llmFunc to record qualifying
// goal evidence on the 3rd LLM call (the one a genuine Resume-driven 3rd
// iteration makes) so a caller can assert real forward progress
// (waiting_on_escalation -> completed) after resuming.
func runToWaitingOnEscalation(t *testing.T, evidenceOnThirdCall bool) (*store.Store, *LoopEngine, *fakeStepExecutor, string, string) {
	t.Helper()
	ctx := context.Background()
	exec := &fakeStepExecutor{}
	st, registry, eng := newLoopEngineTestFixtures(t, exec)
	profile := createTestLoopAgentProfile(t, st, "tick-resume-bridge-agent-"+t.Name())

	goal := store.Goal{Intent: "a target only confirmable by an external check"}
	if err := goal.SetAcceptanceCriteria([]string{"tests pass"}); err != nil {
		t.Fatalf("SetAcceptanceCriteria: %v", err)
	}
	if err := st.CreateGoal(ctx, &goal); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	callCount := 0
	exec.llmFunc = func(req agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error) {
		callCount++
		if evidenceOnThirdCall && callCount == 3 {
			if err := st.RecordGoalEvidence(ctx, &store.GoalEvidence{
				GoalID: goal.ID, EvidenceType: store.GoalEvidenceTypeTestSuite,
				RefTable: "workflow_run_steps", RefID: req.StepID, Summary: "tests pass",
			}); err != nil {
				t.Fatalf("RecordGoalEvidence: %v", err)
			}
		}
		return agentworkflow.LLMStepResult{Text: "ok"}, nil
	}

	wf := oneStepIterationDefinition("tick-resume-bridge-" + t.Name())
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
	return st, eng, exec, result.LoopRunID, profile.ID
}

// TestTickResumeBridge_AttachedReflexTriggerFalse_DoesNotResume proves the
// exact bug the 2026-08-21 escalation diagnosed is closed: a resume_loop_run
// reflex attached to a WAITing LoopRun, whose trigger has not fired,
// genuinely blocks TickResumeBridge.Resume from blind-resuming.
func TestTickResumeBridge_AttachedReflexTriggerFalse_DoesNotResume(t *testing.T) {
	ctx := context.Background()
	st, eng, exec, loopRunID, _ := runToWaitingOnEscalation(t, true)
	callsBeforeResume := exec.calls

	reflexID, err := st.InsertAgentReflex(ctx, store.AgentReflex{
		Name:        "resume-" + loopRunID,
		ClassTag:    "process",
		TriggerKind: store.ReflexTriggerPredicate,
		// scope_tier != "" against an unset (empty) State.ScopeTier is
		// false -- see evalStringEquals's own doc comment.
		TriggerSpec: `{"kind":"scope_tier","value":"some-tier-that-will-never-match"}`,
		ActionKind:  store.ReflexActionResumeLoopRun,
		ActionSpec:  `{"loop_run_id":"` + loopRunID + `"}`,
		CreatedBy:   "system",
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex(resume_loop_run): %v", err)
	}

	reflexEngine := reflexes.NewEngine(st, slog.Default())
	bridge := NewTickResumeBridge(eng, reflexEngine)

	result, err := bridge.Resume(ctx, loopRunID)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if result.Status != store.LoopRunStatusWaitingOnEscalation {
		t.Fatalf("Status = %q, want waiting_on_escalation (the reflex's trigger has not fired)", result.Status)
	}
	if result.CurrentIteration != 2 {
		t.Fatalf("CurrentIteration = %d, want 2 (no new iteration should have launched)", result.CurrentIteration)
	}
	if exec.calls != callsBeforeResume {
		t.Fatalf("exec.calls grew by %d during Resume, want 0 -- Resume must not have blind-resumed", exec.calls-callsBeforeResume)
	}

	// Re-fetch independently from the store -- the same verification style
	// task 11's own end-to-end test uses -- confirming the LoopRun genuinely
	// did not advance, not just that Resume's own return value claims so.
	stillWaiting, err := st.GetLoopRun(ctx, loopRunID)
	if err != nil {
		t.Fatalf("GetLoopRun: %v", err)
	}
	if stillWaiting.Status != store.LoopRunStatusWaitingOnEscalation || stillWaiting.CurrentIteration != 2 {
		t.Fatalf("loop run advanced despite the trigger not firing: status=%q current_iteration=%d",
			stillWaiting.Status, stillWaiting.CurrentIteration)
	}

	// The reflex's own telemetry must show it was evaluated (trigger
	// checked) but did not fire.
	reflexRow, err := st.GetAgentReflex(ctx, reflexID)
	if err != nil {
		t.Fatalf("GetAgentReflex: %v", err)
	}
	if reflexRow.FiredCount != 0 {
		t.Errorf("reflexRow.FiredCount = %d, want 0 (trigger did not fire)", reflexRow.FiredCount)
	}
}

// TestTickResumeBridge_AttachedReflexTriggerTrue_ResumesViaReflexPath is the
// positive case: once the attached reflex's trigger genuinely evaluates
// true, TickResumeBridge.Resume resumes the LoopRun via the real
// resume_loop_run reflex evaluation path (service.
// EvaluateLoopRunResumeReflexes), not a direct/blind Engine.Resume call --
// confirmed both by real forward progress on the re-fetched LoopRun and by
// the reflex's own fired_count telemetry incrementing.
func TestTickResumeBridge_AttachedReflexTriggerTrue_ResumesViaReflexPath(t *testing.T) {
	ctx := context.Background()
	st, eng, exec, loopRunID, _ := runToWaitingOnEscalation(t, true)

	reflexID, err := st.InsertAgentReflex(ctx, store.AgentReflex{
		Name:        "resume-" + loopRunID,
		ClassTag:    "process",
		TriggerKind: store.ReflexTriggerPredicate,
		// An omitted `value` defaults to "" -- scope_tier == "" trivially
		// matches an unset (also "") State.ScopeTier, so this genuinely
		// evaluates true against TickResumeBridge's real headless State{}.
		TriggerSpec: `{"kind":"scope_tier"}`,
		ActionKind:  store.ReflexActionResumeLoopRun,
		ActionSpec:  `{"loop_run_id":"` + loopRunID + `"}`,
		CreatedBy:   "system",
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex(resume_loop_run): %v", err)
	}

	reflexEngine := reflexes.NewEngine(st, slog.Default())
	bridge := NewTickResumeBridge(eng, reflexEngine)

	result, err := bridge.Resume(ctx, loopRunID)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if result.Status != store.LoopRunStatusCompleted {
		t.Fatalf("Status = %q, want completed (the reflex's trigger fired, and the resume-driven 3rd iteration records qualifying goal evidence)", result.Status)
	}
	if result.CurrentIteration != 3 {
		t.Fatalf("CurrentIteration = %d, want 3", result.CurrentIteration)
	}
	if exec.calls != 3 {
		t.Fatalf("exec.calls = %d, want 3 (iterations 1, 2, and the resume-driven 3rd)", exec.calls)
	}

	resumed, err := st.GetLoopRun(ctx, loopRunID)
	if err != nil {
		t.Fatalf("GetLoopRun (after resume): %v", err)
	}
	if resumed.Status != store.LoopRunStatusCompleted {
		t.Fatalf("loop run status = %q, want completed", resumed.Status)
	}
	if resumed.CurrentIteration != 3 {
		t.Fatalf("loop run current_iteration = %d, want 3", resumed.CurrentIteration)
	}

	// This is what actually distinguishes "resumed via the reflex path"
	// from "resumed via a coincidental blind Resume": the reflex's own
	// fired_count telemetry only increments when EvaluateLoopRunResumeReflexes
	// (via reflexes.Resolve -> EmitFirings) genuinely selected and applied
	// this candidate.
	reflexRow, err := st.GetAgentReflex(ctx, reflexID)
	if err != nil {
		t.Fatalf("GetAgentReflex: %v", err)
	}
	if reflexRow.FiredCount != 1 {
		t.Errorf("reflexRow.FiredCount = %d, want 1", reflexRow.FiredCount)
	}
}

// TestTickResumeBridge_NoReflexAttached_FallsBackToBlindResume is the
// negative-negative case: a WAITing LoopRun with zero resume_loop_run
// reflexes attached at all (the plain "durable preset, just retry" case
// tick_schedule.go's own doc comment describes) must still resume via
// TickResumeBridge's fallback to a direct Engine.Resume call -- preserving
// task 12's original default behavior for exactly this case.
func TestTickResumeBridge_NoReflexAttached_FallsBackToBlindResume(t *testing.T) {
	ctx := context.Background()
	st, eng, exec, loopRunID, _ := runToWaitingOnEscalation(t, true)

	reflexEngine := reflexes.NewEngine(st, slog.Default())
	bridge := NewTickResumeBridge(eng, reflexEngine)

	result, err := bridge.Resume(ctx, loopRunID)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if result.Status != store.LoopRunStatusCompleted {
		t.Fatalf("Status = %q, want completed (no reflex attached -- Resume must fall back to a direct blind resume)", result.Status)
	}
	if result.CurrentIteration != 3 {
		t.Fatalf("CurrentIteration = %d, want 3", result.CurrentIteration)
	}
	if exec.calls != 3 {
		t.Fatalf("exec.calls = %d, want 3 (iterations 1, 2, and the resume-driven 3rd)", exec.calls)
	}

	resumed, err := st.GetLoopRun(ctx, loopRunID)
	if err != nil {
		t.Fatalf("GetLoopRun (after resume): %v", err)
	}
	if resumed.Status != store.LoopRunStatusCompleted {
		t.Fatalf("loop run status = %q, want completed (real progress via the blind-resume fallback)", resumed.Status)
	}
	if resumed.CurrentIteration != 3 {
		t.Fatalf("loop run current_iteration = %d, want 3", resumed.CurrentIteration)
	}
}
