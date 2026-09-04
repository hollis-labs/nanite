package service

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
)

type recordingDurableWorkflowHost struct {
	runDefinition agentworkflow.WorkflowDefinition
	resumeRunID   string
	gateRunID     string
	gateStepID    string
	gateInput     string
	gateResponder string
	cancelRunID   string
	cancelReason  string
}

func (h *recordingDurableWorkflowHost) Run(_ context.Context, definition agentworkflow.WorkflowDefinition, _ agentworkflow.WorkflowInput, _ agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error) {
	h.runDefinition = definition
	return agentworkflow.WorkflowResult{RunID: "run-host", Status: agentworkflow.RunStatusCompleted}, nil
}

func (h *recordingDurableWorkflowHost) Resume(_ context.Context, runID string, _ agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error) {
	h.resumeRunID = runID
	return agentworkflow.WorkflowResult{RunID: runID, Status: agentworkflow.RunStatusCompleted}, nil
}

func (h *recordingDurableWorkflowHost) ResumeGate(_ context.Context, runID, stepID, input, responder string, _ agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error) {
	h.gateRunID, h.gateStepID, h.gateInput, h.gateResponder = runID, stepID, input, responder
	return agentworkflow.WorkflowResult{RunID: runID, Status: agentworkflow.RunStatusCompleted}, nil
}

func (h *recordingDurableWorkflowHost) Cancel(_ context.Context, runID, reason string) (agentworkflow.WorkflowResult, error) {
	h.cancelRunID, h.cancelReason = runID, reason
	return agentworkflow.WorkflowResult{RunID: runID, Status: agentworkflow.RunStatusCanceled}, nil
}

func TestWorkflowLauncherUsesSingleHostForEveryCompatibleEngine(t *testing.T) {
	st, profile := newWorkflowLaunchTestFixture(t)
	exec := NewWorkflowStepExecutor(&fakeWorkflowToolService{}, &fakeProviderResolver{}, nil)
	engines := []string{
		"", agentworkflow.EngineBuiltin, agentworkflow.EngineHadron,
		agentworkflow.EngineLangGraph, agentworkflow.EngineCrewAI,
		agentworkflow.EngineGoogleADK, agentworkflow.EngineAutoGen,
		agentworkflow.EngineLangChain,
	}
	for index, engine := range engines {
		definition := singleToolStepWorkflow("one-host-" + engine)
		if engine == "" {
			definition.Name = "one-host-default"
		}
		definition.Engine = engine
		host := &recordingDurableWorkflowHost{}
		launcher := NewWorkflowLauncher(agentworkflow.NewRegistry(nil), host, exec, NewDurableAgentService(st))

		if _, err := launcher.LaunchDefinition(t.Context(), definition, WorkflowLaunchRequest{AgentProfileID: profile.ID}); err != nil {
			t.Fatalf("LaunchDefinition engine[%d]=%q: %v", index, engine, err)
		}
		if host.runDefinition.Name != definition.Name || host.runDefinition.Engine != engine {
			t.Fatalf("host definition = %+v, want name=%q engine=%q", host.runDefinition, definition.Name, engine)
		}
	}
}

func TestWorkflowLauncherRejectsUnknownEngineBeforeDurableLifecycle(t *testing.T) {
	st, profile := newWorkflowLaunchTestFixture(t)
	definition := singleToolStepWorkflow("unknown-engine")
	definition.Engine = "renamed-local-sequencer"
	host := &recordingDurableWorkflowHost{}
	launcher := NewWorkflowLauncher(
		agentworkflow.NewRegistry(map[string]agentworkflow.WorkflowDefinition{definition.Name: definition}),
		host,
		NewWorkflowStepExecutor(&fakeWorkflowToolService{}, &fakeProviderResolver{}, nil),
		NewDurableAgentService(st),
	)

	_, err := launcher.Launch(t.Context(), WorkflowLaunchRequest{WorkflowName: definition.Name, AgentProfileID: profile.ID})
	if err == nil || !strings.Contains(err.Error(), `unsupported engine "renamed-local-sequencer"`) {
		t.Fatalf("Launch error = %v, want unsupported engine", err)
	}
	if host.runDefinition.Name != "" {
		t.Fatalf("host ran invalid definition %+v", host.runDefinition)
	}
	instances, listErr := st.ListDurableAgentInstances(t.Context(), false)
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(instances) != 0 {
		t.Fatalf("durable instances = %+v, want none", instances)
	}
}

func TestWorkflowLauncherControlMethodsUseSharedHost(t *testing.T) {
	host := &recordingDurableWorkflowHost{}
	exec := NewWorkflowStepExecutor(&fakeWorkflowToolService{}, &fakeProviderResolver{}, nil)
	launcher := NewWorkflowLauncher(agentworkflow.NewRegistry(nil), host, exec, nil)

	if _, err := launcher.Resume(t.Context(), "run-1"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if _, err := launcher.ResumeGate(t.Context(), "run-2", "approval", "approved", "a2a-task-1"); err != nil {
		t.Fatalf("ResumeGate: %v", err)
	}
	if _, err := launcher.Cancel(t.Context(), "run-3", "operator canceled"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if host.resumeRunID != "run-1" || host.gateRunID != "run-2" || host.gateStepID != "approval" || host.gateInput != "approved" || host.gateResponder != "a2a-task-1" || host.cancelRunID != "run-3" || host.cancelReason != "operator canceled" {
		t.Fatalf("host control calls = %+v", host)
	}
}

func TestWorkflowLauncherRejectsMissingSharedHost(t *testing.T) {
	st, profile := newWorkflowLaunchTestFixture(t)
	definition := singleToolStepWorkflow("missing-host")
	launcher := NewWorkflowLauncher(
		agentworkflow.NewRegistry(map[string]agentworkflow.WorkflowDefinition{definition.Name: definition}),
		nil,
		NewWorkflowStepExecutor(&fakeWorkflowToolService{}, &fakeProviderResolver{}, nil),
		NewDurableAgentService(st),
	)
	_, err := launcher.Launch(t.Context(), WorkflowLaunchRequest{WorkflowName: definition.Name, AgentProfileID: profile.ID})
	if err == nil || !strings.Contains(err.Error(), "not fully configured") {
		t.Fatalf("Launch error = %v, want not fully configured", err)
	}
}

func TestWorkflowLauncherEnforcesRequiredInputsBeforeHostOrDurableLifecycle(t *testing.T) {
	st, profile := newWorkflowLaunchTestFixture(t)
	definition := singleToolStepWorkflow("required-input")
	definition.Steps[0].Config["args"] = map[string]any{"task": "{{input.task}}"}
	host := &recordingDurableWorkflowHost{}
	launcher := NewWorkflowLauncher(
		agentworkflow.NewRegistry(map[string]agentworkflow.WorkflowDefinition{definition.Name: definition}),
		host,
		NewWorkflowStepExecutor(&fakeWorkflowToolService{}, &fakeProviderResolver{}, nil),
		NewDurableAgentService(st),
	)

	_, err := launcher.Launch(t.Context(), WorkflowLaunchRequest{WorkflowName: definition.Name, AgentProfileID: profile.ID})
	if err == nil || !strings.Contains(err.Error(), "task") {
		t.Fatalf("Launch error = %v, want missing task input", err)
	}
	if host.runDefinition.Name != "" {
		t.Fatalf("host ran definition %+v despite missing input", host.runDefinition)
	}
	instances, err := st.ListDurableAgentInstances(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 0 {
		t.Fatalf("durable instances = %+v, want none", instances)
	}
}
