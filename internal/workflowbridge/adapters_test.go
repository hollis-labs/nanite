package workflowbridge

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/workflowhost"
)

type recordingLoopLauncher struct{ request service.LoopStepLaunchRequest }

func (l *recordingLoopLauncher) LaunchLoop(_ context.Context, request service.LoopStepLaunchRequest) (service.LoopStepLaunchResult, error) {
	l.request = request
	return service.LoopStepLaunchResult{LoopRunID: "loop-1", Status: store.LoopRunStatusWaitingOnEscalation, CurrentIteration: 2}, nil
}

type loopRunReader struct{ run *store.LoopRun }

func (r loopRunReader) GetLoopRun(context.Context, string) (*store.LoopRun, error) { return r.run, nil }

func TestLoopAdapterMapsLaunchKeyAndAuthoritativeObservation(t *testing.T) {
	launcher := &recordingLoopLauncher{}
	adapter := LoopAdapter{Launcher: launcher, Runs: loopRunReader{run: &store.LoopRun{ID: "loop-1", Status: store.LoopRunStatusCompleted, CurrentIteration: 3}}}
	launched, err := adapter.LaunchWorkflowLoop(t.Context(), workflowhost.LoopStepLaunchRequest{
		IdempotencyKey: "run:node", WorkflowName: "iteration", GoalID: "goal-1", AgentProfileID: "profile-1",
		Budget: workflowhost.LoopBudget{MaxIterations: 4, OnExhausted: store.LoopRunOnExhaustedFail},
	})
	if err != nil || launched.LoopRunID != "loop-1" || launcher.request.IdempotencyKey != "run:node" || launcher.request.Budget.MaxIterations != 4 {
		t.Fatalf("LaunchWorkflowLoop = %+v, %v; request=%+v", launched, err, launcher.request)
	}
	observed, err := adapter.ObserveWorkflowLoop(t.Context(), "loop-1")
	if err != nil || observed.Status != store.LoopRunStatusCompleted || observed.CurrentIteration != 3 {
		t.Fatalf("ObserveWorkflowLoop = %+v, %v", observed, err)
	}
}

type recordingTeamResolver struct {
	request  workflowhost.TeamStepResolveRequest
	canceled string
}

func (r *recordingTeamResolver) ResolveWorkflowTeamStep(_ context.Context, runID, stepID string, config map[string]any) (service.WorkflowTeamStepResolution, error) {
	r.request = workflowhost.TeamStepResolveRequest{WorkflowRunID: runID, StepID: stepID, Config: config}
	return service.WorkflowTeamStepResolution{Resolved: true, Output: "ready", ResponderReference: "member-1"}, nil
}

func (r *recordingTeamResolver) CompleteWorkflowTeamStep(_ context.Context, runID, stepID string) error {
	r.request.WorkflowRunID = runID
	r.request.StepID = stepID
	return nil
}

func (r *recordingTeamResolver) CancelWorkflowTeamRun(_ context.Context, runID string) error {
	r.canceled = runID
	return nil
}

func TestTeamAdapterDelegatesNanitePolicy(t *testing.T) {
	resolver := &recordingTeamResolver{}
	result, err := (TeamAdapter{Resolver: resolver}).ResolveWorkflowTeamStep(t.Context(), workflowhost.TeamStepResolveRequest{
		WorkflowRunID: "run-1", StepID: "phase-1", Config: map[string]any{"active_slots": []any{"writer"}},
	})
	if err != nil || !result.Resolved || result.Output != "ready" || result.ResponderReference != "member-1" {
		t.Fatalf("ResolveWorkflowTeamStep = %+v, %v", result, err)
	}
	if resolver.request.WorkflowRunID != "run-1" || resolver.request.StepID != "phase-1" {
		t.Fatalf("resolver request = %+v", resolver.request)
	}
	if err := (TeamAdapter{Resolver: resolver}).CancelWorkflowTeamRun(t.Context(), "run-1"); err != nil || resolver.canceled != "run-1" {
		t.Fatalf("CancelWorkflowTeamRun canceled=%q err=%v", resolver.canceled, err)
	}
}

type recordingExternalEngine struct {
	key, workflow, session string
	params                 map[string]any
}

func (e *recordingExternalEngine) ExecuteWorkflowStep(_ context.Context, key, workflow string, params map[string]any, session string) (service.ExternalWorkflowStepResult, error) {
	e.key, e.workflow, e.params, e.session = key, workflow, params, session
	return service.ExternalWorkflowStepResult{Output: "done"}, nil
}

func TestExternalAdapterForwardsStableIdempotencyKey(t *testing.T) {
	engine := &recordingExternalEngine{}
	result, err := (ExternalAdapter{Engines: map[string]service.ExternalWorkflowStepEngine{"external": engine}}).ExecuteWorkflowExternalStep(t.Context(), workflowhost.ExternalStepRequest{
		IdempotencyKey: "run:external", Engine: "external", WorkflowName: "graph", Params: map[string]any{"input": "value"}, SessionID: "session-1",
	})
	if err != nil || result.IsError || result.Output != "done" {
		t.Fatalf("ExecuteWorkflowExternalStep = %+v, %v", result, err)
	}
	if engine.key != "run:external" || engine.workflow != "graph" || engine.params["input"] != "value" || engine.session != "session-1" {
		t.Fatalf("external call = %+v", engine)
	}
}
