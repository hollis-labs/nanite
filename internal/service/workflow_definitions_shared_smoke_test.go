package service

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/workflowhost"
)

func exampleWorkflowDefinitionsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "examples", "workflow-definitions")
}

func TestWorkflowDefinitionsSharedSmokeWorkerReviewerGate(t *testing.T) {
	registry, err := agentworkflow.LoadRegistryDir(exampleWorkflowDefinitionsDir(t))
	if err != nil {
		t.Fatalf("load example definitions: %v", err)
	}
	definition, ok := registry.Get("worker-reviewer-gate")
	if !ok {
		t.Fatalf("worker-reviewer-gate missing from %v", registry.Names())
	}

	productStore, profile := newWorkflowLaunchTestFixture(t)
	state, err := workflowhost.NewWorkflowStateStore(productStore)
	if err != nil {
		t.Fatal(err)
	}
	host, err := workflowhost.NewEngine(state)
	if err != nil {
		t.Fatal(err)
	}
	provider := &scriptedProvider{responses: [][]llmtypes.StreamEvent{
		{
			{Type: llmtypes.EventDelta, Content: "Task: reverse the string abc. Result: cba"},
			{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{StopReason: "end_turn"}},
		},
		{
			{Type: llmtypes.EventDelta, Content: "PASS the output correctly reverses the stated string"},
			{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{StopReason: "end_turn"}},
		},
	}}
	executor := NewWorkflowStepExecutor(
		&fakeWorkflowToolService{},
		&fakeProviderResolver{providers: map[string]llmcontracts.Provider{"anthropic": provider}},
		nil,
	)
	launcher := NewWorkflowLauncher(registry, host, executor, NewDurableAgentService(productStore))

	result, err := launcher.Launch(context.Background(), WorkflowLaunchRequest{
		WorkflowName: definition.Name, AgentProfileID: profile.ID,
		Params: map[string]any{"task": "reverse the string abc"},
	})
	if err != nil {
		t.Fatalf("launch shared example: %v", err)
	}
	if result.Status != agentworkflow.RunStatusWaiting {
		t.Fatalf("status = %q, want waiting_on_gate", result.Status)
	}
	if provider.callCount != 2 {
		t.Fatalf("provider calls = %d, want worker plus reviewer", provider.callCount)
	}
	worker, ok := result.StepResults["worker"]
	if !ok || worker.IsError || worker.VerifyResult == nil || !worker.VerifyResult.Passed {
		t.Fatalf("worker result = %+v, present=%v", worker, ok)
	}
	steps, err := productStore.ListWorkflowRunSteps(t.Context(), result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	statuses := make(map[string]string, len(steps))
	for _, step := range steps {
		statuses[step.StepID] = step.Status
	}
	if statuses["worker"] != "completed" || statuses["approve"] != "waiting_on_gate" {
		t.Fatalf("persisted step statuses = %v", statuses)
	}
}
