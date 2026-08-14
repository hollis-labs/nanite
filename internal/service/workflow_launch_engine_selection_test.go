package service

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
)

// recordingWorkflowEngine records whether Run was invoked — enough to
// distinguish "the launcher picked this engine" from "it picked some other
// one" without needing a real subprocess.
type recordingWorkflowEngine struct {
	name string
	ran  bool
}

func (e *recordingWorkflowEngine) Name() string { return e.name }

func (e *recordingWorkflowEngine) Run(context.Context, agentworkflow.WorkflowDefinition, agentworkflow.WorkflowInput, agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error) {
	e.ran = true
	return agentworkflow.WorkflowResult{Status: agentworkflow.RunStatusCompleted, StepResults: map[string]agentworkflow.StepResult{}}, nil
}

var _ agentworkflow.WorkflowEngine = (*recordingWorkflowEngine)(nil)

func externalTargetedWorkflow(name, engine string) agentworkflow.WorkflowDefinition {
	wf := singleToolStepWorkflow(name)
	wf.Engine = engine
	return wf
}

// TestWorkflowLauncher_Launch_EmptyEngine_DefaultsToBuiltin proves a
// WorkflowDefinition with no Engine set resolves to agentworkflow.
// EngineBuiltin — the whole point of the field being optional.
func TestWorkflowLauncher_Launch_EmptyEngine_DefaultsToBuiltin(t *testing.T) {
	st, profile := newWorkflowLaunchTestFixture(t)
	registry := agentworkflow.NewRegistry(map[string]agentworkflow.WorkflowDefinition{
		"noop-workflow": singleToolStepWorkflow("noop-workflow"),
	})
	builtin := &recordingWorkflowEngine{name: agentworkflow.EngineBuiltin}
	other := &recordingWorkflowEngine{name: agentworkflow.EngineLangGraph}
	exec := NewWorkflowStepExecutor(&fakeWorkflowToolService{}, &fakeProviderResolver{}, nil)
	launcher := NewWorkflowLauncher(registry, map[string]agentworkflow.WorkflowEngine{
		agentworkflow.EngineBuiltin:    builtin,
		agentworkflow.EngineLangGraph: other,
	}, exec, NewDurableAgentService(st))

	if _, err := launcher.Launch(context.Background(), WorkflowLaunchRequest{
		WorkflowName:   "noop-workflow",
		WorkspaceID:    "ws-launch",
		AgentProfileID: profile.ID,
	}); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if !builtin.ran {
		t.Error("builtin engine did not run")
	}
	if other.ran {
		t.Error("langgraph engine ran, want it untouched")
	}
}

// TestWorkflowLauncher_Launch_ExplicitEngine_RoutesToIt proves a
// WorkflowDefinition naming a non-default engine actually reaches that
// engine's Run, not the built-in one.
func TestWorkflowLauncher_Launch_ExplicitEngine_RoutesToIt(t *testing.T) {
	st, profile := newWorkflowLaunchTestFixture(t)
	registry := agentworkflow.NewRegistry(map[string]agentworkflow.WorkflowDefinition{
		"langgraph-workflow": externalTargetedWorkflow("langgraph-workflow", agentworkflow.EngineLangGraph),
	})
	builtin := &recordingWorkflowEngine{name: agentworkflow.EngineBuiltin}
	langgraph := &recordingWorkflowEngine{name: agentworkflow.EngineLangGraph}
	exec := NewWorkflowStepExecutor(&fakeWorkflowToolService{}, &fakeProviderResolver{}, nil)
	launcher := NewWorkflowLauncher(registry, map[string]agentworkflow.WorkflowEngine{
		agentworkflow.EngineBuiltin:    builtin,
		agentworkflow.EngineLangGraph: langgraph,
	}, exec, NewDurableAgentService(st))

	if _, err := launcher.Launch(context.Background(), WorkflowLaunchRequest{
		WorkflowName:   "langgraph-workflow",
		WorkspaceID:    "ws-launch",
		AgentProfileID: profile.ID,
	}); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if builtin.ran {
		t.Error("builtin engine ran, want it untouched")
	}
	if !langgraph.ran {
		t.Error("langgraph engine did not run")
	}
}

// TestWorkflowLauncher_Launch_UnregisteredEngine_ClearError proves a
// WorkflowDefinition naming an engine this launcher has no entry for fails
// with a clear, engine-naming error rather than a nil-map panic or a
// silent fall-through to the built-in engine.
func TestWorkflowLauncher_Launch_UnregisteredEngine_ClearError(t *testing.T) {
	st, profile := newWorkflowLaunchTestFixture(t)
	registry := agentworkflow.NewRegistry(map[string]agentworkflow.WorkflowDefinition{
		"crewai-workflow": externalTargetedWorkflow("crewai-workflow", agentworkflow.EngineCrewAI),
	})
	builtin := &recordingWorkflowEngine{name: agentworkflow.EngineBuiltin}
	exec := NewWorkflowStepExecutor(&fakeWorkflowToolService{}, &fakeProviderResolver{}, nil)
	launcher := NewWorkflowLauncher(registry, builtinEngineSet(builtin), exec, NewDurableAgentService(st))

	_, err := launcher.Launch(context.Background(), WorkflowLaunchRequest{
		WorkflowName:   "crewai-workflow",
		WorkspaceID:    "ws-launch",
		AgentProfileID: profile.ID,
	})
	if err == nil {
		t.Fatal("expected an error for an unregistered engine")
	}
	if !strings.Contains(err.Error(), "crewai") {
		t.Fatalf("err = %v, want it to name the missing engine %q", err, agentworkflow.EngineCrewAI)
	}
	if builtin.ran {
		t.Error("builtin engine ran, want Launch to fail before reaching any engine")
	}
}
