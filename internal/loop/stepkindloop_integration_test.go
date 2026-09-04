package loop

// TASKS/loops/09-stepkindloop-executor-and-waiting-status.md's own required
// end-to-end integration test — quoted directly from that task file: "the
// phase-closure race stress test analog Teams' own
// 06-stepkindflex-executor.md was required to cover for flex — treat it
// with the same weight, not a passing mention." A WorkflowDefinition with a
// StepKindLoop step whose contained LoopDefinition is a trivial,
// 1-2-iteration bounded loop:
//
//  1. reaches RunStatusWaitingOnLoop when the step starts (iteration 1's
//     own WorkflowRun contains a gate, so the LoopRun itself pauses
//     waiting_on_gate rather than completing synchronously within the
//     first LaunchLoop call — otherwise the outer run would go straight to
//     RunStatusCompleted without ever genuinely sitting in
//     RunStatusWaitingOnLoop, which would defeat the whole point of this
//     test);
//  2. the outer WorkflowRun genuinely transitions out of that status once
//     the contained LoopRun completes — via the real push mechanism
//     (OuterResumeNotifier.NotifyLoopRunTerminal, called by LoopEngine
//     itself the moment it persists loop_runs.status = completed), never
//     via a manually-triggered test-only outer .Resume() call standing in
//     for it. This test only ever calls Resume on the INNER LoopRun
//     (simulating a real external trigger resolving the inner gate — a
//     legitimate real caller shape per docs/engineering/architecture/
//     21-loops.md's "Trigger surface": human resolution / a reflex firing
//     / a scheduled tick); the outer WorkflowRun's own .Resume call is
//     made by LoopResumeNotifier, internal/service/
//     workflow_loop_adapter.go, from inside that same LoopEngine.Resume
//     call — never by this test directly.
//
// This lives in package loop because it composes a real shared workflow host,
// the product bridge, and a real LoopEngine without introducing a production
// package cycle.

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/workflowbridge"
	"github.com/hollis-labs/nanite/internal/workflowhost"
)

func TestStepKindLoop_OuterWorkflowRun_ResolvesViaRealPush_NotManualResume(t *testing.T) {
	ctx := context.Background()
	st := newTestLoopStore(t)
	registry := agentworkflow.NewRegistry(nil)
	exec := &fakeStepExecutor{}

	state, err := workflowhost.NewWorkflowStateStore(st)
	if err != nil {
		t.Fatalf("NewWorkflowStateStore: %v", err)
	}
	host, err := workflowhost.NewEngine(state)
	if err != nil {
		t.Fatalf("workflowhost.NewEngine: %v", err)
	}
	durable := service.NewDurableAgentService(st)
	launcher := service.NewWorkflowLauncher(registry, host, exec, durable)

	loopEngine := NewLoopEngine(st, registry, launcher)
	notifier := service.NewLoopResumeNotifier(st, launcher)
	loopEngine.WithOuterResumeNotifier(notifier)
	host.WithLoopStepHost(workflowbridge.LoopAdapter{Launcher: loopEngine, Runs: st})

	profile := createTestLoopAgentProfile(t, st, "stepkindloop-agent")

	// Empty acceptance_criteria/constraints/invariants: EvidenceSatisfiesGoal's
	// four-clause formula (internal/store/goal_evidence.go) is vacuously
	// satisfied by an empty criteria list, so a single recorded evidence
	// row (below, after the gate resolves) is enough to make Decide return
	// COMPLETE on iteration 1's own evaluation, without needing a second
	// iteration.
	goal := store.Goal{Intent: "stepkindloop trivial bounded loop"}
	if err := st.CreateGoal(ctx, &goal); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	// Inner iteration definition: a single gate step, so the LoopRun's
	// first iteration pauses (loop_runs.status -> waiting_on_gate) instead
	// of completing synchronously within the very first LaunchLoop call —
	// see this file's own package doc comment for why that distinction
	// matters for this test.
	innerWF := agentworkflow.WorkflowDefinition{
		Name: "stepkindloop-inner-iteration",
		Steps: []agentworkflow.StepDefinition{
			{ID: "approve", Kind: agentworkflow.StepKindGate},
		},
	}
	if err := registry.Register(innerWF); err != nil {
		t.Fatalf("Register(inner): %v", err)
	}

	// Outer definition: a single StepKindLoop step. LoopResumeNotifier
	// needs this registered too (by name) to look it up when the LoopRun
	// eventually goes terminal.
	outerWF := agentworkflow.WorkflowDefinition{
		Name: "stepkindloop-outer",
		Steps: []agentworkflow.StepDefinition{
			{
				ID:   "run_loop",
				Kind: agentworkflow.StepKindLoop,
				Config: map[string]any{
					"workflow_name":    innerWF.Name,
					"agent_profile_id": profile.ID,
					"goal_id":          goal.ID,
					"budget": map[string]any{
						"max_iterations": 2,
						"on_exhausted":   store.LoopRunOnExhaustedEscalate,
					},
				},
			},
		},
	}
	if err := registry.Register(outerWF); err != nil {
		t.Fatalf("Register(outer): %v", err)
	}

	// --- Step 1: launch the outer run; it must reach RunStatusWaitingOnLoop ---

	result, err := host.Run(ctx, outerWF, agentworkflow.WorkflowInput{}, exec)
	if err != nil {
		t.Fatalf("outer Run: %v", err)
	}
	if result.Status != agentworkflow.RunStatusWaitingOnLoop {
		t.Fatalf("outer run Status = %q, want %q", result.Status, agentworkflow.RunStatusWaitingOnLoop)
	}
	outerRunID := result.RunID

	steps, err := st.ListWorkflowRunSteps(context.Background(), outerRunID)
	if err != nil {
		t.Fatalf("ListWorkflowRunSteps: %v", err)
	}
	var loopStepRow *store.WorkflowRunStepRow
	for _, row := range steps {
		if row.StepID == "run_loop" {
			loopStepRow = row
		}
	}
	if loopStepRow == nil {
		t.Fatal("run_loop step row not persisted")
	}
	if loopStepRow.Status != "waiting_on_loop" {
		t.Fatalf("run_loop step status = %q, want waiting_on_loop", loopStepRow.Status)
	}
	if loopStepRow.LoopRunID == nil || *loopStepRow.LoopRunID == "" {
		t.Fatal("run_loop step has no loop_run_id recorded")
	}
	loopRunID := *loopStepRow.LoopRunID

	lr, err := st.GetLoopRun(ctx, loopRunID)
	if err != nil {
		t.Fatalf("GetLoopRun: %v", err)
	}
	if lr.Status != store.LoopRunStatusWaitingOnGate {
		t.Fatalf("loop_run status = %q, want waiting_on_gate (iteration 1's own gate step)", lr.Status)
	}

	// Confirm the outer workflow_runs row itself is also genuinely
	// persisted as waiting_on_loop, not just the in-memory WorkflowResult.
	outerRunRow, err := st.GetWorkflowRun(context.Background(), outerRunID)
	if err != nil {
		t.Fatalf("GetWorkflowRun(outer): %v", err)
	}
	if outerRunRow.Status != string(agentworkflow.RunStatusWaitingOnLoop) {
		t.Fatalf("persisted outer workflow_runs.status = %q, want %q", outerRunRow.Status, agentworkflow.RunStatusWaitingOnLoop)
	}

	// --- Step 2: resolve the inner gate and record qualifying evidence,
	// then resume ONLY the inner LoopRun — never the outer run directly ---

	iterations, err := st.ListLoopRunIterations(ctx, loopRunID)
	if err != nil {
		t.Fatalf("ListLoopRunIterations: %v", err)
	}
	if len(iterations) != 1 {
		t.Fatalf("len(iterations) = %d, want 1", len(iterations))
	}
	innerRunID := iterations[0].WorkflowRunID
	if innerRunID == "" {
		t.Fatal("iteration 1 has no workflow_run_id")
	}

	if _, resumeErr := host.ResumeGate(context.Background(), innerRunID, "approve", "approved", "test-operator", exec); resumeErr != nil {
		t.Fatalf("ResumeGate: %v", resumeErr)
	}
	if err := st.RecordGoalEvidence(ctx, &store.GoalEvidence{
		GoalID: goal.ID, EvidenceType: store.GoalEvidenceTypeGateApproval,
		RefTable: "workflow_run_steps", RefID: "approve", Summary: "gate approved",
	}); err != nil {
		t.Fatalf("RecordGoalEvidence: %v", err)
	}

	resumed, err := loopEngine.Resume(ctx, loopRunID)
	if err != nil {
		t.Fatalf("loopEngine.Resume: %v", err)
	}
	if resumed.Status != store.LoopRunStatusCompleted {
		t.Fatalf("resumed loop_run Status = %q, want completed (last decision %+v)", resumed.Status, resumed.LastDecision)
	}
	if resumed.LastDecision.Kind != DecisionComplete {
		t.Fatalf("resumed LastDecision.Kind = %q, want complete", resumed.LastDecision.Kind)
	}

	// --- Step 3: the real assertion — the OUTER workflow_run must have
	// genuinely transitioned out of waiting_on_loop, purely as a side
	// effect of the LoopRun going terminal (NotifyLoopRunTerminal calling
	// the shared host's Resume internally) — this test never resumes the outer
	// run directly. ---

	outerRunAfter, err := st.GetWorkflowRun(context.Background(), outerRunID)
	if err != nil {
		t.Fatalf("GetWorkflowRun(outer) after loop terminal: %v", err)
	}
	if outerRunAfter.Status != "completed" {
		t.Fatalf("outer workflow_runs.status after the real push = %q, want completed — the outer run never left waiting_on_loop via the notifier", outerRunAfter.Status)
	}

	stepsAfter, err := st.ListWorkflowRunSteps(context.Background(), outerRunID)
	if err != nil {
		t.Fatalf("ListWorkflowRunSteps after loop terminal: %v", err)
	}
	var loopStepAfter *store.WorkflowRunStepRow
	for _, row := range stepsAfter {
		if row.StepID == "run_loop" {
			loopStepAfter = row
		}
	}
	if loopStepAfter == nil {
		t.Fatal("run_loop step row missing after loop terminal")
	}
	if loopStepAfter.Status != "completed" {
		t.Fatalf("run_loop step status after the real push = %q, want completed", loopStepAfter.Status)
	}
	if loopStepAfter.IsError {
		t.Fatalf("run_loop step IsError = true after the real push, want false: output=%q", loopStepAfter.Output)
	}
}
