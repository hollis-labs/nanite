package loop

// TASKS/loops/09-stepkindloop-executor-and-waiting-status.md — the load-
// bearing import-cycle fix for the "outer workflow launches a Loop"
// direction: internal/service's StepKindLoop step executor
// (startLoopStep, internal/service/workflow_engine_loop.go) cannot import
// internal/loop to call LoopEngine.Run directly, because internal/loop
// already imports internal/service (this package's own engine.go, task
// 08's WorkflowLauncher dependency) — internal/service importing back
// would be internal/service -> internal/loop -> internal/service, a real
// cycle.
//
// The fix: internal/service declares a narrow LoopStepLauncher interface
// speaking only its own request/result types (LoopStepLaunchRequest/
// LoopStepLaunchResult, workflow_engine_loop.go), and this file makes
// *LoopEngine satisfy it structurally — legal because THIS package
// already imports internal/service, so this method can freely reference
// those types without creating the reverse edge.

import (
	"context"

	"github.com/hollis-labs/nanite/internal/service"
)

// LaunchLoop implements service.LoopStepLauncher — see this file's own
// package doc comment for why this bridge method, rather than a direct
// internal/service import of internal/loop, is what resolves the cycle.
// Translates service's own request/result shapes to/from this package's
// real LoopDefinition/LoopInput/LoopResult and calls Run — a StepKindLoop
// step only ever launches a brand-new LoopRun (never resumes an existing
// one; a later Resume of an in-flight LoopRun is driven externally — task
// 10/11/12's own callers, or this task's own end-to-end test — not by the
// outer step re-entering runStep, which never happens for an already-
// waiting_on_loop step).
func (e *LoopEngine) LaunchLoop(ctx context.Context, req service.LoopStepLaunchRequest) (service.LoopStepLaunchResult, error) {
	def := LoopDefinition{WorkflowName: req.WorkflowName}
	input := LoopInput{
		GoalID: req.GoalID,
		Budget: req.Budget,
		ContinuationPolicy: ContinuationPolicy{
			Provider:  req.ContinuationPolicy.Provider,
			Model:     req.ContinuationPolicy.Model,
			AgentID:   req.ContinuationPolicy.AgentID,
			SessionID: req.ContinuationPolicy.SessionID,
			Tools:     req.ContinuationPolicy.Tools,
		},
		WorkflowParams:  req.WorkflowParams,
		AgentProfileID:  req.AgentProfileID,
		ProjectID:       req.ProjectID,
		ParentSessionID: req.ParentSessionID,
		TimeoutSeconds:  req.TimeoutSeconds,
	}
	if req.GoalID == "" {
		input.Goal = &LoopGoalSpec{
			ParentGoalID:       req.GoalParentID,
			Intent:             req.GoalIntent,
			DesiredState:       req.GoalDesiredState,
			Constraints:        req.GoalConstraints,
			AcceptanceCriteria: req.GoalAcceptanceCriteria,
			Invariants:         req.GoalInvariants,
			Priority:           req.GoalPriority,
			Scope:              req.GoalScope,
			Owner:              req.GoalOwner,
			Source:             req.GoalSource,
		}
	}

	result, err := e.Run(ctx, def, input)
	if err != nil {
		return service.LoopStepLaunchResult{}, err
	}
	return service.LoopStepLaunchResult{
		LoopRunID:        result.LoopRunID,
		Status:           result.Status,
		CurrentIteration: result.CurrentIteration,
	}, nil
}

var _ service.LoopStepLauncher = (*LoopEngine)(nil)
