package loop

// TASKS/loops/13-loop-presets.md's own "Done means" coverage:
//
//  1. Every named preset in the design doc's list (ralph, test-fix,
//     review-fix, plan-execute, queue-drain, durable, self-improve) is
//     registered and resolvable via GetPreset, even the stubbed ones.
//  2. "ralph" launches and runs to completion end-to-end (via
//     LoopLauncher.Launch with DefinitionName: "ralph") in a regression
//     test with a stubbed StepExecutor, terminating correctly on
//     MaxIterations exhaustion (ESCALATE, per OnExhausted) in one test case
//     and on a real COMPLETE signal in another -- two genuinely distinct
//     test cases, not one test asserting both outcomes about the same run.
//  3. A stubbed preset (e.g. "test-fix") still resolves via GetPreset, but
//     actually launching it returns ErrPresetNotImplemented.
//
// Live-verification safety (EXECUTION-PROCESS.md): every test below reuses
// engine_test.go's own real, t.TempDir()-rooted SQLite *store.Store fixture
// helpers (newLoopEngineTestFixtures/createTestLoopAgentProfile) and
// decide_test.go's own fakeStepExecutor stub -- no real LLM call, same
// test-double convention this package's own engine/launcher tests already
// use.

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
)

// ralphWorkflowParams is the fixed set of WorkflowParams every "ralph"
// launch below supplies -- see presets.go's own package doc comment
// ("Ralph's WorkflowDefinition template") for why exactly these four keys
// are required.
func ralphWorkflowParams(profileID, task string) map[string]any {
	return map[string]any{
		RalphParamTask:     task,
		RalphParamProvider: "anthropic",
		RalphParamModel:    "claude-sonnet-test",
		RalphParamAgentID:  profileID,
	}
}

// --- Done means #1: every named preset resolves via GetPreset ---

func TestGetPreset_EveryDesignDocPresetNameResolves(t *testing.T) {
	names := []string{
		PresetRalph, PresetTestFix, PresetReviewFix, PresetPlanExecute,
		PresetQueueDrain, PresetDurable, PresetSelfImprove,
	}
	for _, name := range names {
		p, ok := GetPreset(name)
		if !ok {
			t.Fatalf("GetPreset(%q) ok = false, want true", name)
		}
		if p.Name != name {
			t.Fatalf("GetPreset(%q).Name = %q, want %q", name, p.Name, name)
		}
	}
	if _, ok := GetPreset("not-a-real-preset"); ok {
		t.Fatalf("GetPreset(unknown) ok = true, want false")
	}
}

func TestGetPreset_RalphImplemented_OthersAreStubs(t *testing.T) {
	ralph, ok := GetPreset(PresetRalph)
	if !ok || !ralph.implemented() {
		t.Fatalf("ralph preset: ok=%v implemented=%v, want true/true", ok, ralph.implemented())
	}

	stubs := []string{
		PresetTestFix, PresetReviewFix, PresetPlanExecute,
		PresetQueueDrain, PresetDurable, PresetSelfImprove,
	}
	for _, name := range stubs {
		p, ok := GetPreset(name)
		if !ok {
			t.Fatalf("GetPreset(%q) ok = false, want true", name)
		}
		if p.implemented() {
			t.Fatalf("stub preset %q: implemented() = true, want false (this task's own stub convention)", name)
		}
	}
}

func TestPresetNames_Sorted(t *testing.T) {
	got := PresetNames()
	want := []string{
		PresetDurable, PresetPlanExecute, PresetQueueDrain, PresetRalph,
		PresetReviewFix, PresetSelfImprove, PresetTestFix,
	}
	if len(got) != len(want) {
		t.Fatalf("PresetNames() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("PresetNames()[%d] = %q, want %q (want sorted order %v)", i, got[i], want[i], want)
		}
	}
}

// --- Done means #2a: ralph terminates via ESCALATE on MaxIterations exhaustion ---

func TestLoopLauncher_Launch_Ralph_EscalatesOnBudgetExhaustion(t *testing.T) {
	ctx := context.Background()
	exec := &fakeStepExecutor{}
	st, _, eng := newLoopEngineTestFixtures(t, exec)
	profile := createTestLoopAgentProfile(t, st, "ralph-escalate-agent")
	launcher := NewLoopLauncher(eng, st)

	// No goal evidence is ever recorded, so goal_met never fires.
	// BudgetOverrides tightens ralphPreset's own default (MaxIterations 20)
	// down to 1, so budgetExhausted trips immediately after the first
	// iteration's evaluation -- landing on ESCALATE, per
	// store.LoopRunOnExhaustedEscalate (ralphPreset's own default
	// OnExhausted, left un-overridden here).
	result, err := launcher.Launch(ctx, LoopLaunchRequest{
		InlineGoal: &GoalSpec{
			Intent:             "an unreachable ralph target",
			AcceptanceCriteria: []string{"unreachable criterion"},
		},
		DefinitionName:  PresetRalph,
		AgentProfileID:  profile.ID,
		WorkflowParams:  ralphWorkflowParams(profile.ID, "do the unreachable thing"),
		BudgetOverrides: &store.Budget{MaxIterations: 1},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if result.Status != store.LoopRunStatusWaitingOnEscalation {
		t.Fatalf("Status = %q, want waiting_on_escalation (last decision %+v)", result.Status, result.LastDecision)
	}
	if result.LastDecision.Kind != DecisionEscalate {
		t.Fatalf("LastDecision.Kind = %q, want escalate", result.LastDecision.Kind)
	}
	if result.CurrentIteration != 1 {
		t.Fatalf("CurrentIteration = %d, want 1", result.CurrentIteration)
	}

	lr, err := st.GetLoopRun(ctx, result.LoopRunID)
	if err != nil {
		t.Fatalf("GetLoopRun: %v", err)
	}
	if lr.Status != store.LoopRunStatusWaitingOnEscalation {
		t.Fatalf("persisted loop_run status = %q, want waiting_on_escalation", lr.Status)
	}
	if lr.DefinitionName != PresetRalph {
		t.Fatalf("loop_run definition_name = %q, want %q", lr.DefinitionName, PresetRalph)
	}

	iterations, err := st.ListLoopRunIterations(ctx, result.LoopRunID)
	if err != nil {
		t.Fatalf("ListLoopRunIterations: %v", err)
	}
	if len(iterations) != 1 {
		t.Fatalf("len(iterations) = %d, want 1", len(iterations))
	}
	if iterations[0].ProgressState != store.LoopRunIterationProgressProgress {
		t.Fatalf("iteration progress_state = %q, want progress (ralph's own no-verify shape is optimistic PROGRESS)", iterations[0].ProgressState)
	}
}

// --- Done means #2b: ralph terminates via a real COMPLETE signal ---

func TestLoopLauncher_Launch_Ralph_CompletesOnGoalMet(t *testing.T) {
	ctx := context.Background()
	exec := &fakeStepExecutor{}
	st, _, eng := newLoopEngineTestFixtures(t, exec)
	profile := createTestLoopAgentProfile(t, st, "ralph-complete-agent")
	launcher := NewLoopLauncher(eng, st)

	// Pre-created (not an inline goal spec) so its ID is known up front for
	// exec.llmFunc's own RecordGoalEvidence call below -- mirrors
	// engine_test.go's TestLoopEngine_Run_MultiIterationLoop_CompletesOnGoalMet
	// convention exactly.
	goal := store.Goal{Intent: "ship the ralph feature"}
	if err := goal.SetAcceptanceCriteria([]string{"tests pass"}); err != nil {
		t.Fatalf("SetAcceptanceCriteria: %v", err)
	}
	if err := st.CreateGoal(ctx, &goal); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	exec.llmFunc = func(req agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error) {
		// The iteration's own step is what produces the qualifying evidence
		// -- proves Decide's goal-met branch reacts to evidence recorded
		// during the very iteration being evaluated.
		if err := st.RecordGoalEvidence(ctx, &store.GoalEvidence{
			GoalID: goal.ID, EvidenceType: store.GoalEvidenceTypeTestSuite,
			RefTable: "workflow_run_steps", RefID: req.StepID, Summary: "tests pass",
		}); err != nil {
			t.Fatalf("RecordGoalEvidence: %v", err)
		}
		return agentworkflow.LLMStepResult{Text: "ok"}, nil
	}

	goalID := goal.ID
	result, err := launcher.Launch(ctx, LoopLaunchRequest{
		GoalID:         &goalID,
		DefinitionName: PresetRalph,
		AgentProfileID: profile.ID,
		WorkflowParams: ralphWorkflowParams(profile.ID, "do the reachable thing"),
		// No BudgetOverrides -- exercises ralphPreset's own default Budget
		// (MaxIterations 20, OnExhausted escalate, MaxNoProgressIterations
		// 0) applied automatically by resolvePresetDefaults, not a
		// caller-supplied one. If this ever regressed to needing an
		// explicit override, this test would start timing out against 20
		// real iterations instead of completing on iteration 1 --
		// confirming the preset default really is being applied.
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if result.Status != store.LoopRunStatusCompleted {
		t.Fatalf("Status = %q, want completed (last decision %+v)", result.Status, result.LastDecision)
	}
	if result.LastDecision.Kind != DecisionComplete {
		t.Fatalf("LastDecision.Kind = %q, want complete", result.LastDecision.Kind)
	}
	if result.CurrentIteration != 1 {
		t.Fatalf("CurrentIteration = %d, want 1", result.CurrentIteration)
	}

	lr, err := st.GetLoopRun(ctx, result.LoopRunID)
	if err != nil {
		t.Fatalf("GetLoopRun: %v", err)
	}
	if lr.Status != store.LoopRunStatusCompleted {
		t.Fatalf("persisted loop_run status = %q, want completed", lr.Status)
	}
	if lr.CompletedAt == "" {
		t.Fatalf("loop_run completed_at is empty, want set")
	}
}

// --- Idempotent registration: launching "ralph" twice must not error ---

func TestLoopLauncher_Launch_Ralph_TwiceDoesNotErrorOnRegistration(t *testing.T) {
	ctx := context.Background()
	exec := &fakeStepExecutor{}
	st, _, eng := newLoopEngineTestFixtures(t, exec)
	profile := createTestLoopAgentProfile(t, st, "ralph-twice-agent")
	launcher := NewLoopLauncher(eng, st)

	launchOnce := func(intent string) LoopResult {
		result, err := launcher.Launch(ctx, LoopLaunchRequest{
			InlineGoal:      &GoalSpec{Intent: intent, AcceptanceCriteria: []string{"unreachable"}},
			DefinitionName:  PresetRalph,
			AgentProfileID:  profile.ID,
			WorkflowParams:  ralphWorkflowParams(profile.ID, "do the thing"),
			BudgetOverrides: &store.Budget{MaxIterations: 1},
		})
		if err != nil {
			t.Fatalf("Launch(%q): %v", intent, err)
		}
		return result
	}

	first := launchOnce("first ralph run")
	second := launchOnce("second ralph run")
	if first.LoopRunID == second.LoopRunID {
		t.Fatalf("two distinct Launch calls returned the same LoopRunID %q", first.LoopRunID)
	}
	if first.Status != store.LoopRunStatusWaitingOnEscalation || second.Status != store.LoopRunStatusWaitingOnEscalation {
		t.Fatalf("Status = %q / %q, want waiting_on_escalation for both", first.Status, second.Status)
	}
}

// --- Done means #3: a stubbed preset resolves but fails to launch cleanly ---

func TestLoopLauncher_Launch_StubPreset_ReturnsNotImplemented(t *testing.T) {
	ctx := context.Background()
	exec := &fakeStepExecutor{}
	st, _, eng := newLoopEngineTestFixtures(t, exec)
	profile := createTestLoopAgentProfile(t, st, "stub-preset-agent")
	launcher := NewLoopLauncher(eng, st)

	for _, name := range []string{PresetTestFix, PresetReviewFix, PresetPlanExecute, PresetQueueDrain, PresetDurable, PresetSelfImprove} {
		_, err := launcher.Launch(ctx, LoopLaunchRequest{
			InlineGoal:     &GoalSpec{Intent: "stub launch attempt " + name},
			DefinitionName: name,
			AgentProfileID: profile.ID,
		})
		if !errors.Is(err, ErrPresetNotImplemented) {
			t.Fatalf("Launch(%q) error = %v, want ErrPresetNotImplemented", name, err)
		}
	}
}

// --- Preset-name-first precedence does not disturb the existing, unrelated
// ordinary-WorkflowDefinition-name launch path ---

func TestLoopLauncher_Launch_NonPresetName_StillFallsBackToRegistry(t *testing.T) {
	ctx := context.Background()
	exec := &fakeStepExecutor{}
	st, registry, eng := newLoopEngineTestFixtures(t, exec)
	profile := createTestLoopAgentProfile(t, st, "non-preset-agent")
	launcher := NewLoopLauncher(eng, st)

	wf := oneStepIterationDefinition("an-ordinary-non-preset-definition")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}

	result, err := launcher.Launch(ctx, LoopLaunchRequest{
		InlineGoal:      &GoalSpec{Intent: "ordinary definition launch"},
		DefinitionName:  wf.Name,
		AgentProfileID:  profile.ID,
		BudgetOverrides: &store.Budget{MaxIterations: 1},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	lr, err := st.GetLoopRun(ctx, result.LoopRunID)
	if err != nil {
		t.Fatalf("GetLoopRun: %v", err)
	}
	if lr.DefinitionName != wf.Name {
		t.Fatalf("loop_run definition_name = %q, want %q", lr.DefinitionName, wf.Name)
	}
}
