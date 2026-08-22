package service

// CW-20260815-0005: end-to-end smoke coverage for the shipped
// Worker → Reviewer → Gate WorkflowDefinition (the Agent Roles design
// doc's core dogfooding artifact — previously only a Go test fixture in
// internal/agentworkflow/definition_yaml_test.go, never a real, loadable
// definition). This test proves the example at
// examples/workflow-definitions/worker-reviewer-gate.yaml:
//
//   - loads and validates via the same agentworkflow.LoadRegistryDir path
//     production wiring uses (cmd/nanite/main.go, gated on
//     config.WorkflowDefinitionsPath) — a real file on disk, not an
//     in-memory literal;
//   - actually launches end to end through the real
//     WorkflowLauncher/BuiltinWorkflowEngine/WorkflowStepExecutor stack —
//     not just a static ParseDefinitionYAML/Validate pass — reaching a
//     genuine terminal outcome (Status: waiting_on_gate) with the worker
//     step's independent reviewer verify recorded as passed.
//
// A scripted LLM provider stands in for both the worker and reviewer
// turns, so this needs no live Anthropic credentials — mirrors the seam
// internal/service/workflow_launch_test.go and workflow_step_executor_test.go
// already use.
//
// What it does NOT do: exercise the workflow_run self-tool's MCP-layer
// caller-profile trust resolution (internal/mcp/self_tools_workflow_run.go)
// or resolve the gate itself — there is no approve-gate tool/endpoint in
// this codebase yet, so "waiting_on_gate" is the real, intended terminal
// outcome this test asserts, not an artifact of an incomplete run.

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/agentworkflow"
)

// exampleWorkflowDefinitionsDir locates examples/workflow-definitions/
// relative to this test source file so the test runs regardless of cwd.
// Mirrors chat_bootprofile_smoke_test.go's exampleCatalogDir.
func exampleWorkflowDefinitionsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	return filepath.Join(repoRoot, "examples", "workflow-definitions")
}

// TestWorkflowDefinitionsSmoke_RegistryLoadsAndValidates is the
// load-bearing "does the example parse + validate?" check. If a future
// change to the agentworkflow schema breaks the example, this fails
// loudly — the example doubles as schema documentation for the Worker →
// Reviewer → Gate shape, so silent breakage would leave the design doc's
// referenced artifact lying.
func TestWorkflowDefinitionsSmoke_RegistryLoadsAndValidates(t *testing.T) {
	dir := exampleWorkflowDefinitionsDir(t)
	registry, err := agentworkflow.LoadRegistryDir(dir)
	if err != nil {
		t.Fatalf("LoadRegistryDir(%s) = %v, want nil", dir, err)
	}

	wf, ok := registry.Get("worker-reviewer-gate")
	if !ok {
		t.Fatalf("workflow %q missing from registry (got %v)", "worker-reviewer-gate", registry.Names())
	}
	if len(wf.Steps) != 2 {
		t.Fatalf("len(Steps) = %d, want 2", len(wf.Steps))
	}

	worker := wf.Steps[0]
	if worker.ID != "worker" || worker.Kind != agentworkflow.StepKindLLM {
		t.Fatalf("worker step = %+v", worker)
	}
	if worker.Verify == nil || worker.Verify.Mode != agentworkflow.VerifyModeAgent {
		t.Fatalf("worker.Verify = %+v, want mode=agent", worker.Verify)
	}

	gate := wf.Steps[1]
	if gate.ID != "approve" || gate.Kind != agentworkflow.StepKindGate {
		t.Fatalf("gate step = %+v", gate)
	}
	if len(gate.DependsOn) != 1 || gate.DependsOn[0] != "worker" {
		t.Fatalf("gate.DependsOn = %v, want [worker]", gate.DependsOn)
	}
}

// TestWorkflowDefinitionsSmoke_LaunchesEndToEnd proves the example is not
// just statically valid but actually launchable: a real WorkflowLauncher
// run, through the real BuiltinWorkflowEngine and WorkflowStepExecutor,
// reaches the gate with the reviewer's independent verify recorded as
// passed.
func TestWorkflowDefinitionsSmoke_LaunchesEndToEnd(t *testing.T) {
	dir := exampleWorkflowDefinitionsDir(t)
	registry, err := agentworkflow.LoadRegistryDir(dir)
	if err != nil {
		t.Fatalf("LoadRegistryDir(%s) = %v, want nil", dir, err)
	}

	st, profile := newWorkflowLaunchTestFixture(t)

	// Two scripted turns: the worker's answer, then the independent
	// reviewer's PASS verdict — exactly the two ExecuteLLMStep calls
	// worker-reviewer-gate.yaml's verify:{mode:agent} modifier triggers.
	prov := &scriptedProvider{responses: [][]llmtypes.StreamEvent{
		{
			{Type: llmtypes.EventDelta, Content: "Task: reverse the string \"abc\".\n\nResult: \"cba\""},
			{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{StopReason: "end_turn"}},
		},
		{
			{Type: llmtypes.EventDelta, Content: "PASS the output correctly reverses the stated string"},
			{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{StopReason: "end_turn"}},
		},
	}}
	resolver := &fakeProviderResolver{providers: map[string]llmcontracts.Provider{"anthropic": prov}}
	exec := NewWorkflowStepExecutor(&fakeWorkflowToolService{}, resolver, nil)
	engine := NewBuiltinWorkflowEngine(st)
	durable := NewDurableAgentService(st)
	launcher := NewWorkflowLauncher(registry, builtinEngineSet(engine), exec, durable)

	result, err := launcher.Launch(context.Background(), WorkflowLaunchRequest{
		WorkflowName:   "worker-reviewer-gate",
		AgentProfileID: profile.ID,
		Params:         map[string]any{"task": "reverse the string \"abc\""},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if result.Status != agentworkflow.RunStatusWaiting {
		t.Fatalf("Status = %q, want waiting_on_gate (a paused gate is the real, intended terminal outcome — no approve-gate surface exists yet)", result.Status)
	}
	if prov.callCount != 2 {
		t.Fatalf("provider calls = %d, want 2 (worker turn + independent reviewer turn)", prov.callCount)
	}

	worker, ok := result.StepResults["worker"]
	if !ok {
		t.Fatal("StepResults[worker] missing")
	}
	if worker.IsError {
		t.Fatalf("worker step IsError = true, want false: %+v", worker)
	}
	if worker.VerifyResult == nil || !worker.VerifyResult.Passed {
		t.Fatalf("worker.VerifyResult = %+v, want a passed independent-reviewer verdict", worker.VerifyResult)
	}

	steps, err := st.ListWorkflowRunSteps(context.Background(), result.RunID)
	if err != nil {
		t.Fatalf("ListWorkflowRunSteps: %v", err)
	}
	byID := map[string]string{}
	for _, s := range steps {
		byID[s.StepID] = s.Status
	}
	if byID["worker"] != "completed" {
		t.Errorf("worker persisted status = %q, want completed", byID["worker"])
	}
	if byID["approve"] != "waiting_on_gate" {
		t.Errorf("approve persisted status = %q, want waiting_on_gate", byID["approve"])
	}
}
