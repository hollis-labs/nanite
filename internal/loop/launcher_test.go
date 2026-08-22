package loop

// TASKS/loops/10-loop-launcher-and-api.md's own "Done means" coverage:
//
//  1. LoopLauncher.Launch end-to-end: inline-goal launch creates both the
//     Goal and the LoopRun; existing-goal-id launch reuses the Goal; a
//     second launch against a goal_id with an already-active LoopRun is
//     rejected with a clear error (loop.ErrLoopRunAlreadyActive).
//  2. ResolveEscalation tested for all three override modes
//     (force_complete, force_cancel, replan) plus the no-override (resume
//     normally) case, against a LoopRun genuinely parked in
//     waiting_on_escalation.
//
// Live-verification safety (EXECUTION-PROCESS.md): every test below reuses
// engine_test.go's own real, t.TempDir()-rooted SQLite *store.Store fixture
// helpers (newTestLoopStore/newLoopEngineTestFixtures/
// createTestLoopAgentProfile/oneStepIterationDefinition) and decide_test.go's
// own fakeStepExecutor stub -- no real LLM call, same test-double
// convention this package's own engine tests already use.
//
// forceEscalation (below) is this file's own small fixture helper: a
// Budget{MaxIterations: 1} with no recorded goal evidence drives exactly
// one iteration to budgetExhausted's own ESCALATE branch (decide.go),
// landing the freshly-launched LoopRun in waiting_on_escalation -- the
// precondition every ResolveEscalation test below needs.

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// forceEscalation launches a fresh Goal+LoopRun under def, driven by exec,
// with a budget that escalates after exactly one iteration (no goal
// evidence is ever recorded, so goal_met never fires; MaxIterations: 1
// makes budgetExhausted's own len(history) >= budget.MaxIterations check
// trip immediately after that first iteration's evaluation). Returns the
// resulting waiting_on_escalation LoopRunID.
func forceEscalation(t *testing.T, ctx context.Context, st *store.Store, eng *LoopEngine, defName, profileID string) string {
	t.Helper()
	goal := store.Goal{Intent: "escalation fixture goal " + defName}
	if err := st.CreateGoal(ctx, &goal); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	result, err := eng.Run(ctx, LoopDefinition{WorkflowName: defName}, LoopInput{
		GoalID:         goal.ID,
		AgentProfileID: profileID,
		Budget:         store.Budget{MaxIterations: 1},
	})
	if err != nil {
		t.Fatalf("Run (force escalation): %v", err)
	}
	if result.Status != store.LoopRunStatusWaitingOnEscalation {
		t.Fatalf("Status = %q, want waiting_on_escalation (last decision %+v)", result.Status, result.LastDecision)
	}
	return result.LoopRunID
}

// --- Launch ---

func TestLoopLauncher_Launch_InlineGoal_CreatesGoalAndLoopRun(t *testing.T) {
	ctx := context.Background()
	exec := &fakeStepExecutor{}
	st, registry, eng := newLoopEngineTestFixtures(t, exec)
	profile := createTestLoopAgentProfile(t, st, "launcher-inline-agent")
	launcher := NewLoopLauncher(eng, st)

	wf := oneStepIterationDefinition("launcher-inline-def")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}

	result, err := launcher.Launch(ctx, LoopLaunchRequest{
		InlineGoal: &GoalSpec{
			Intent:             "ship the inline goal",
			AcceptanceCriteria: []string{"tests pass"},
		},
		DefinitionName: wf.Name,
		AgentProfileID: profile.ID,
		// Bounded on purpose: no goal evidence is ever recorded in this
		// test, so goal_met never fires -- an unbounded (zero-value)
		// Budget here would make Decide return CONTINUE forever (this
		// file's own budgetExhausted doc comment: "MaxIterations==0 and
		// MaxRuntimeSeconds==0 is an explicit, intentional 'run until
		// COMPLETE/FAIL' configuration"). This test only cares that
		// Launch produced a real Goal+LoopRun, not that the loop reaches
		// any particular terminal status.
		BudgetOverrides: &store.Budget{MaxIterations: 1},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if result.LoopRunID == "" {
		t.Fatalf("Launch returned empty LoopRunID")
	}

	lr, err := st.GetLoopRun(ctx, result.LoopRunID)
	if err != nil {
		t.Fatalf("GetLoopRun: %v", err)
	}
	goal, err := st.GetGoal(ctx, lr.GoalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if goal.Intent != "ship the inline goal" {
		t.Fatalf("goal.Intent = %q, want %q", goal.Intent, "ship the inline goal")
	}
}

func TestLoopLauncher_Launch_ExistingGoalID_ReusesGoal(t *testing.T) {
	ctx := context.Background()
	exec := &fakeStepExecutor{}
	st, registry, eng := newLoopEngineTestFixtures(t, exec)
	profile := createTestLoopAgentProfile(t, st, "launcher-existing-agent")
	launcher := NewLoopLauncher(eng, st)

	goal := store.Goal{Intent: "pre-existing goal"}
	if err := st.CreateGoal(ctx, &goal); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	wf := oneStepIterationDefinition("launcher-existing-def")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}

	goalID := goal.ID
	result, err := launcher.Launch(ctx, LoopLaunchRequest{
		GoalID:         &goalID,
		DefinitionName: wf.Name,
		AgentProfileID: profile.ID,
		// Bounded on purpose -- see the identical comment on
		// TestLoopLauncher_Launch_InlineGoal_CreatesGoalAndLoopRun above.
		BudgetOverrides: &store.Budget{MaxIterations: 1},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	lr, err := st.GetLoopRun(ctx, result.LoopRunID)
	if err != nil {
		t.Fatalf("GetLoopRun: %v", err)
	}
	if lr.GoalID != goal.ID {
		t.Fatalf("lr.GoalID = %q, want %q (should reuse the existing goal, not create a new one)", lr.GoalID, goal.ID)
	}
}

func TestLoopLauncher_Launch_SecondLaunchAgainstActiveGoal_Rejected(t *testing.T) {
	ctx := context.Background()
	exec := &fakeStepExecutor{}
	st, registry, eng := newLoopEngineTestFixtures(t, exec)
	profile := createTestLoopAgentProfile(t, st, "launcher-conflict-agent")
	launcher := NewLoopLauncher(eng, st)

	goal := store.Goal{Intent: "conflict goal"}
	if err := st.CreateGoal(ctx, &goal); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	wf := oneStepIterationDefinition("launcher-conflict-def")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}

	goalID := goal.ID
	// First launch escalates after one iteration (no goal evidence,
	// MaxIterations: 1) -- waiting_on_escalation is one of
	// store.LoopRunActiveStatuses, so the LoopRun stays "active" for the
	// purposes of the one-active-LoopRun-per-goal_id check.
	if _, err := launcher.Launch(ctx, LoopLaunchRequest{
		GoalID:          &goalID,
		DefinitionName:  wf.Name,
		AgentProfileID:  profile.ID,
		BudgetOverrides: &store.Budget{MaxIterations: 1},
	}); err != nil {
		t.Fatalf("first Launch: %v", err)
	}

	_, err := launcher.Launch(ctx, LoopLaunchRequest{
		GoalID:         &goalID,
		DefinitionName: wf.Name,
		AgentProfileID: profile.ID,
	})
	if !errors.Is(err, ErrLoopRunAlreadyActive) {
		t.Fatalf("second Launch error = %v, want ErrLoopRunAlreadyActive", err)
	}
}

func TestLoopLauncher_Launch_NotConfigured(t *testing.T) {
	var launcher *LoopLauncher
	if _, err := launcher.Launch(context.Background(), LoopLaunchRequest{}); err == nil {
		t.Fatalf("Launch on a nil launcher: want error, got nil")
	}
}

// --- Cancel ---

func TestLoopLauncher_Cancel(t *testing.T) {
	ctx := context.Background()
	exec := &fakeStepExecutor{}
	st, registry, eng := newLoopEngineTestFixtures(t, exec)
	profile := createTestLoopAgentProfile(t, st, "launcher-cancel-agent")
	launcher := NewLoopLauncher(eng, st)

	wf := oneStepIterationDefinition("launcher-cancel-def")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}
	loopRunID := forceEscalation(t, ctx, st, eng, wf.Name, profile.ID)

	if err := launcher.Cancel(ctx, loopRunID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	lr, err := st.GetLoopRun(ctx, loopRunID)
	if err != nil {
		t.Fatalf("GetLoopRun: %v", err)
	}
	if lr.Status != store.LoopRunStatusCancelled {
		t.Fatalf("Status = %q, want cancelled", lr.Status)
	}
	if lr.CompletedAt == "" {
		t.Fatalf("CompletedAt not set after Cancel")
	}
}

func TestLoopLauncher_Cancel_UnknownID(t *testing.T) {
	ctx := context.Background()
	exec := &fakeStepExecutor{}
	st, _, eng := newLoopEngineTestFixtures(t, exec)
	launcher := NewLoopLauncher(eng, st)

	err := launcher.Cancel(ctx, "does-not-exist")
	if !errors.Is(err, store.ErrLoopRunNotFound) {
		t.Fatalf("Cancel unknown id error = %v, want ErrLoopRunNotFound", err)
	}
}

// --- ResolveEscalation ---

func TestLoopLauncher_ResolveEscalation_NoOverride_ResumesNormally(t *testing.T) {
	ctx := context.Background()
	exec := &fakeStepExecutor{}
	st, registry, eng := newLoopEngineTestFixtures(t, exec)
	profile := createTestLoopAgentProfile(t, st, "resolve-noop-agent")
	launcher := NewLoopLauncher(eng, st)

	wf := oneStepIterationDefinition("resolve-noop-def")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}
	loopRunID := forceEscalation(t, ctx, st, eng, wf.Name, profile.ID)

	result, err := launcher.ResolveEscalation(ctx, loopRunID, nil)
	if err != nil {
		t.Fatalf("ResolveEscalation: %v", err)
	}
	// Resume re-enters Decide with the same MaxIterations: 1 budget and no
	// new evidence -- it launches a second iteration, then immediately
	// escalates again (budgetExhausted still trips: len(history) now 2 >=
	// MaxIterations 1). The proof this "resumed normally" (re-entered
	// Decide, not a no-op) is CurrentIteration advancing to 2.
	if result.CurrentIteration != 2 {
		t.Fatalf("CurrentIteration = %d, want 2 (Resume should have launched a second iteration)", result.CurrentIteration)
	}
	if result.Status != store.LoopRunStatusWaitingOnEscalation {
		t.Fatalf("Status = %q, want waiting_on_escalation", result.Status)
	}
}

func TestLoopLauncher_ResolveEscalation_EmptyOverride_ResumesNormally(t *testing.T) {
	ctx := context.Background()
	exec := &fakeStepExecutor{}
	st, registry, eng := newLoopEngineTestFixtures(t, exec)
	profile := createTestLoopAgentProfile(t, st, "resolve-empty-agent")
	launcher := NewLoopLauncher(eng, st)

	wf := oneStepIterationDefinition("resolve-empty-def")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}
	loopRunID := forceEscalation(t, ctx, st, eng, wf.Name, profile.ID)

	// A non-nil override with every field left at its zero value (the
	// shape POST /api/loops/{id}/resolve's own bare `{}` body decodes to)
	// must behave identically to a nil override.
	result, err := launcher.ResolveEscalation(ctx, loopRunID, &EscalationOverride{})
	if err != nil {
		t.Fatalf("ResolveEscalation: %v", err)
	}
	if result.CurrentIteration != 2 {
		t.Fatalf("CurrentIteration = %d, want 2", result.CurrentIteration)
	}
}

func TestLoopLauncher_ResolveEscalation_ForceComplete(t *testing.T) {
	ctx := context.Background()
	exec := &fakeStepExecutor{}
	st, registry, eng := newLoopEngineTestFixtures(t, exec)
	profile := createTestLoopAgentProfile(t, st, "resolve-complete-agent")
	launcher := NewLoopLauncher(eng, st)

	wf := oneStepIterationDefinition("resolve-complete-def")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}
	loopRunID := forceEscalation(t, ctx, st, eng, wf.Name, profile.ID)

	result, err := launcher.ResolveEscalation(ctx, loopRunID, &EscalationOverride{ForceComplete: true})
	if err != nil {
		t.Fatalf("ResolveEscalation: %v", err)
	}
	if result.Status != store.LoopRunStatusCompleted {
		t.Fatalf("Status = %q, want completed", result.Status)
	}
	lr, err := st.GetLoopRun(ctx, loopRunID)
	if err != nil {
		t.Fatalf("GetLoopRun: %v", err)
	}
	if lr.Status != store.LoopRunStatusCompleted {
		t.Fatalf("persisted Status = %q, want completed", lr.Status)
	}
	if lr.CompletedAt == "" {
		t.Fatalf("CompletedAt not set after force_complete")
	}
	// force_complete bypasses Decide entirely -- no new iteration launched.
	if lr.CurrentIteration != 1 {
		t.Fatalf("CurrentIteration = %d, want 1 (force_complete must not launch a new iteration)", lr.CurrentIteration)
	}
}

func TestLoopLauncher_ResolveEscalation_ForceCancel(t *testing.T) {
	ctx := context.Background()
	exec := &fakeStepExecutor{}
	st, registry, eng := newLoopEngineTestFixtures(t, exec)
	profile := createTestLoopAgentProfile(t, st, "resolve-cancel-agent")
	launcher := NewLoopLauncher(eng, st)

	wf := oneStepIterationDefinition("resolve-cancel-def")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}
	loopRunID := forceEscalation(t, ctx, st, eng, wf.Name, profile.ID)

	result, err := launcher.ResolveEscalation(ctx, loopRunID, &EscalationOverride{ForceCancel: true})
	if err != nil {
		t.Fatalf("ResolveEscalation: %v", err)
	}
	if result.Status != store.LoopRunStatusCancelled {
		t.Fatalf("Status = %q, want cancelled", result.Status)
	}
	lr, err := st.GetLoopRun(ctx, loopRunID)
	if err != nil {
		t.Fatalf("GetLoopRun: %v", err)
	}
	if lr.Status != store.LoopRunStatusCancelled {
		t.Fatalf("persisted Status = %q, want cancelled", lr.Status)
	}
}

func TestLoopLauncher_ResolveEscalation_Replan(t *testing.T) {
	ctx := context.Background()
	exec := &fakeStepExecutor{}
	st, registry, eng := newLoopEngineTestFixtures(t, exec)
	profile := createTestLoopAgentProfile(t, st, "resolve-replan-agent")
	launcher := NewLoopLauncher(eng, st)

	wf := oneStepIterationDefinition("resolve-replan-def")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}
	replanWF := oneStepIterationDefinition("resolve-replan-revised-def")
	if err := registry.Register(replanWF); err != nil {
		t.Fatalf("Register (revised def): %v", err)
	}
	loopRunID := forceEscalation(t, ctx, st, eng, wf.Name, profile.ID)

	lrBefore, err := st.GetLoopRun(ctx, loopRunID)
	if err != nil {
		t.Fatalf("GetLoopRun: %v", err)
	}
	goalBefore, err := st.GetGoal(ctx, lrBefore.GoalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}

	result, err := launcher.ResolveEscalation(ctx, loopRunID, &EscalationOverride{
		Replan: &ReplanOverride{
			DefinitionName:     replanWF.Name,
			DesiredState:       []string{"revised desired state"},
			AcceptanceCriteria: []string{"revised acceptance criteria"},
		},
	})
	if err != nil {
		t.Fatalf("ResolveEscalation: %v", err)
	}
	// The revised budget is still MaxIterations: 1 with no new evidence, so
	// Resume launches a second iteration and immediately re-escalates --
	// the proof the replan was genuinely applied (not a no-op) is the
	// definition_name/goal fields below, not the resulting status.
	if result.CurrentIteration != 2 {
		t.Fatalf("CurrentIteration = %d, want 2", result.CurrentIteration)
	}

	lrAfter, err := st.GetLoopRun(ctx, loopRunID)
	if err != nil {
		t.Fatalf("GetLoopRun (after): %v", err)
	}
	if lrAfter.DefinitionName != replanWF.Name {
		t.Fatalf("DefinitionName = %q, want %q (replan override should persist)", lrAfter.DefinitionName, replanWF.Name)
	}

	goalAfter, err := st.GetGoal(ctx, lrBefore.GoalID)
	if err != nil {
		t.Fatalf("GetGoal (after): %v", err)
	}
	if goalAfter.DesiredStateJSON == goalBefore.DesiredStateJSON {
		t.Fatalf("goal DesiredStateJSON unchanged after replan override")
	}
	desired, err := goalAfter.DesiredState()
	if err != nil {
		t.Fatalf("DesiredState: %v", err)
	}
	if len(desired) != 1 || desired[0] != "revised desired state" {
		t.Fatalf("DesiredState = %v, want [revised desired state]", desired)
	}
}

func TestLoopLauncher_ResolveEscalation_RejectsWhenNotWaitingOnEscalation(t *testing.T) {
	ctx := context.Background()
	exec := &fakeStepExecutor{}
	st, registry, eng := newLoopEngineTestFixtures(t, exec)
	profile := createTestLoopAgentProfile(t, st, "resolve-notwaiting-agent")
	launcher := NewLoopLauncher(eng, st)

	// A goal_met COMPLETE run never passes through waiting_on_escalation.
	goal := store.Goal{Intent: "already satisfied goal"}
	if err := goal.SetAcceptanceCriteria([]string{"done"}); err != nil {
		t.Fatalf("SetAcceptanceCriteria: %v", err)
	}
	if err := st.CreateGoal(ctx, &goal); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if err := st.RecordGoalEvidence(ctx, &store.GoalEvidence{
		GoalID: goal.ID, EvidenceType: store.GoalEvidenceTypeTestSuite,
		RefTable: "workflow_run_steps", RefID: "step-1", Summary: "done",
	}); err != nil {
		t.Fatalf("RecordGoalEvidence: %v", err)
	}

	wf := oneStepIterationDefinition("resolve-notwaiting-def")
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
		t.Fatalf("Status = %q, want completed", result.Status)
	}

	_, err = launcher.ResolveEscalation(ctx, result.LoopRunID, nil)
	if !errors.Is(err, ErrLoopRunNotWaitingOnEscalation) {
		t.Fatalf("ResolveEscalation on a completed run: err = %v, want ErrLoopRunNotWaitingOnEscalation", err)
	}
}

func TestLoopLauncher_ResolveEscalation_UnknownID(t *testing.T) {
	ctx := context.Background()
	exec := &fakeStepExecutor{}
	st, _, eng := newLoopEngineTestFixtures(t, exec)
	launcher := NewLoopLauncher(eng, st)

	_, err := launcher.ResolveEscalation(ctx, "does-not-exist", nil)
	if !errors.Is(err, store.ErrLoopRunNotFound) {
		t.Fatalf("ResolveEscalation unknown id error = %v, want ErrLoopRunNotFound", err)
	}
}
