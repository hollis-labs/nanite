// Package workflowbridge contains the acyclic product adapters between the
// shared workflow host and Nanite services. It is deliberately a leaf: the
// workflow host never imports service or loop packages.
package workflowbridge

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/workflowhost"
)

// LoopAdapter translates the shared StepKind boundary onto Nanite's existing
// LoopRun launcher and authoritative store projection.
type LoopAdapter struct {
	Launcher service.LoopStepLauncher
	Runs     service.LoopRunStatusStore
}

func (a LoopAdapter) LaunchWorkflowLoop(ctx context.Context, request workflowhost.LoopStepLaunchRequest) (workflowhost.LoopStepResult, error) {
	if a.Launcher == nil {
		return workflowhost.LoopStepResult{}, errors.New("workflow loop bridge: launcher is not configured")
	}
	launch := service.LoopStepLaunchRequest{
		IdempotencyKey: request.IdempotencyKey,
		WorkflowName:   request.WorkflowName, GoalID: request.GoalID,
		Budget: store.Budget{
			MaxIterations: request.Budget.MaxIterations, MaxFailures: request.Budget.MaxFailures,
			MaxRuntimeSeconds:       request.Budget.MaxRuntimeSeconds,
			MaxNoProgressIterations: request.Budget.MaxNoProgressIterations,
			OnExhausted:             request.Budget.OnExhausted,
		},
		ContinuationPolicy: service.LoopStepContinuationPolicy{
			Provider: request.ContinuationPolicy.Provider, Model: request.ContinuationPolicy.Model,
			AgentID: request.ContinuationPolicy.AgentID, SessionID: request.ContinuationPolicy.SessionID,
			Tools: append([]string(nil), request.ContinuationPolicy.Tools...),
		},
		WorkflowParams: request.WorkflowParams, AgentProfileID: request.AgentProfileID,
		ProjectID: request.ProjectID, ParentSessionID: request.ParentSessionID, TimeoutSeconds: request.TimeoutSeconds,
	}
	if request.Goal != nil {
		launch.GoalParentID = request.Goal.ParentGoalID
		launch.GoalIntent = request.Goal.Intent
		launch.GoalDesiredState = append([]string(nil), request.Goal.DesiredState...)
		launch.GoalConstraints = append([]string(nil), request.Goal.Constraints...)
		launch.GoalAcceptanceCriteria = append([]string(nil), request.Goal.AcceptanceCriteria...)
		launch.GoalInvariants = append([]string(nil), request.Goal.Invariants...)
		launch.GoalPriority = request.Goal.Priority
		launch.GoalScope = request.Goal.Scope
		launch.GoalOwner = request.Goal.Owner
		launch.GoalSource = request.Goal.Source
	}
	result, err := a.Launcher.LaunchLoop(ctx, launch)
	if err != nil {
		return workflowhost.LoopStepResult{}, err
	}
	return workflowhost.LoopStepResult{LoopRunID: result.LoopRunID, Status: result.Status, CurrentIteration: result.CurrentIteration}, nil
}

func (a LoopAdapter) ObserveWorkflowLoop(ctx context.Context, id string) (workflowhost.LoopStepResult, error) {
	if a.Runs == nil {
		return workflowhost.LoopStepResult{}, errors.New("workflow loop bridge: run store is not configured")
	}
	run, err := a.Runs.GetLoopRun(ctx, id)
	if err != nil {
		return workflowhost.LoopStepResult{}, err
	}
	return workflowhost.LoopStepResult{LoopRunID: run.ID, Status: run.Status, CurrentIteration: run.CurrentIteration}, nil
}

// TeamStepResolver is the service-native team phase evaluation seam.
type TeamStepResolver interface {
	ResolveWorkflowTeamStep(context.Context, string, string, map[string]any) (service.WorkflowTeamStepResolution, error)
	CompleteWorkflowTeamStep(context.Context, string, string) error
	CancelWorkflowTeamRun(context.Context, string) error
}

func (a TeamAdapter) CompleteWorkflowTeamStep(ctx context.Context, runID, stepID string) error {
	if a.Resolver == nil {
		return errors.New("workflow team bridge: resolver is not configured")
	}
	return a.Resolver.CompleteWorkflowTeamStep(ctx, runID, stepID)
}

func (a TeamAdapter) CancelWorkflowTeamRun(ctx context.Context, runID string) error {
	if a.Resolver == nil {
		return errors.New("workflow team bridge: resolver is not configured")
	}
	return a.Resolver.CancelWorkflowTeamRun(ctx, runID)
}

type TeamAdapter struct{ Resolver TeamStepResolver }

func (a TeamAdapter) ResolveWorkflowTeamStep(ctx context.Context, request workflowhost.TeamStepResolveRequest) (workflowhost.TeamStepResolveResult, error) {
	if a.Resolver == nil {
		return workflowhost.TeamStepResolveResult{}, errors.New("workflow team bridge: resolver is not configured")
	}
	result, err := a.Resolver.ResolveWorkflowTeamStep(ctx, request.WorkflowRunID, request.StepID, request.Config)
	if err != nil {
		return workflowhost.TeamStepResolveResult{}, err
	}
	if result.IsError {
		return workflowhost.TeamStepResolveResult{}, fmt.Errorf("workflow team bridge: %s", result.Output)
	}
	return workflowhost.TeamStepResolveResult{
		Resolved: result.Resolved, Output: result.Output, ResponderReference: result.ResponderReference,
	}, nil
}

// ExternalAdapter invokes legacy external framework runners as a single
// keyed shared-host step. The stable key is forwarded in params so each
// external implementation can use it as its durable side-effect key.
type ExternalAdapter struct {
	Engines map[string]service.ExternalWorkflowStepEngine
}

func (a ExternalAdapter) ExecuteWorkflowExternalStep(ctx context.Context, request workflowhost.ExternalStepRequest) (workflowhost.ExternalStepResult, error) {
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return workflowhost.ExternalStepResult{}, errors.New("workflow external bridge: idempotency key is required")
	}
	engine, ok := a.Engines[request.Engine]
	if !ok || engine == nil {
		return workflowhost.ExternalStepResult{}, fmt.Errorf("workflow external bridge: unknown engine %q", request.Engine)
	}
	result, err := engine.ExecuteWorkflowStep(ctx, request.IdempotencyKey, request.WorkflowName, request.Params, request.SessionID)
	if err != nil {
		return workflowhost.ExternalStepResult{}, err
	}
	return workflowhost.ExternalStepResult{Output: result.Output, IsError: result.IsError}, nil
}

var (
	_ workflowhost.LoopStepHost     = LoopAdapter{}
	_ workflowhost.TeamStepHost     = TeamAdapter{}
	_ workflowhost.ExternalStepHost = ExternalAdapter{}
)
