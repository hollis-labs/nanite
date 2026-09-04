package service

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"testing"

	workflowruntime "github.com/hollis-labs/go-workflow/runtime"

	"github.com/hollis-labs/nanite/internal/a2a"
	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/nanite/internal/workflowhost"
)

// TestA2AGateIntegration verifies the full gate ↔ input-required flow:
//  1. Workflow with a gate reaches waiting_on_gate status
//  2. Task state derives to input-required
//  3. Providing input resolves the gate
//  4. Workflow resumes and completes
//
// CW-20260814-0017: A2A gate ↔ input-required Task state mapping.
func TestA2AGateIntegration(t *testing.T) {
	// This is a smoke test skeleton. Full implementation requires:
	//  - A test store with workflow_runs + workflow_run_steps
	//  - A fake StepExecutor that simulates gate pausing
	//  - TaskManager wired up with a WorkflowLauncher
	//
	// The acceptance criteria verification will be done manually or via
	// integration tests that can run the full stack.

	t.Skip("Integration test - requires full workflow stack wiring")

	// Placeholder structure for what the test would do:
	//
	// 1. Create a workflow definition with a gate step
	// wf := agentworkflow.WorkflowDefinition{
	//     Name: "test-gate-flow",
	//     Steps: []agentworkflow.StepDefinition{
	//         {ID: "step1", Kind: agentworkflow.StepKindTool},
	//         {ID: "gate1", Kind: agentworkflow.StepKindGate, DependsOn: []string{"step1"}},
	//         {ID: "step2", Kind: agentworkflow.StepKindTool, DependsOn: []string{"gate1"}},
	//     },
	// }
	//
	// 2. Submit a task that launches this workflow
	// taskMgr := NewTaskManager(...)
	// result := taskMgr.SubmitTask(ctx, TaskSubmitRequest{Target: "test-gate-flow", ...})
	//
	// 3. Poll until task reaches input-required
	// task := taskMgr.GetTask(ctx, result.TaskID)
	// if task.State != a2a.TaskStateInputRequired { t.Fatal(...) }
	//
	// 4. Provide input to resolve the gate
	// err := taskMgr.ProvideTaskInput(ctx, result.TaskID, "approved")
	//
	// 5. Verify workflow resumes and completes
	// task = taskMgr.GetTask(ctx, result.TaskID)
	// if task.State != a2a.TaskStateCompleted { t.Fatal(...) }
}

func TestA2AHadronGateInputResumesExactRunAndPreservesIdentity(t *testing.T) {
	st := newTestStore(t)
	state, err := workflowhost.NewWorkflowStateStore(st)
	if err != nil {
		t.Fatal(err)
	}
	waits := &workflowruntime.WaitCoordinator{Store: state, Authorizer: workflowhost.NaniteResponderAuthorizer{}}
	engine, err := workflowhost.NewEngine(state)
	if err != nil {
		t.Fatal(err)
	}
	engine.WithWaitCoordinator(waits)

	const taskID = "a2a-hadron-gate-task"
	definition := agentworkflow.WorkflowDefinition{
		Name: "A2A Hadron approval flow", Engine: agentworkflow.EngineHadron,
		Steps: []agentworkflow.StepDefinition{
			{ID: "approve release", Kind: agentworkflow.StepKindGate},
			{ID: "publish", Kind: agentworkflow.StepKindTool, DependsOn: []string{"approve release"}, Config: map[string]any{
				"tool": "publish", "agent_id": "release-agent", "args": map[string]any{"approval": "{{steps.approve release.output}}"},
			}},
		},
	}
	executor := &a2aHadronStepExecutor{}
	waiting, err := engine.Run(t.Context(), definition, agentworkflow.WorkflowInput{
		Params: map[string]any{"_nanite_a2a_task_id": taskID}, SessionID: "a2a-workflow-session",
	}, executor)
	if err != nil || waiting.Status != agentworkflow.RunStatusWaiting {
		t.Fatalf("Run = %+v, %v", waiting, err)
	}
	registry := agentworkflow.NewRegistry(map[string]agentworkflow.WorkflowDefinition{definition.Name: definition})
	launcher := NewWorkflowLauncher(registry, engine, executor, nil)
	tm := &TaskManager{store: st, launcher: launcher, registry: registry, logger: testLogger(t), pushNotifier: NewA2APushNotifier(st, testLogger(t))}
	if createErr := st.CreateA2ATask(t.Context(), &store.A2ATask{
		ID: taskID, TargetKind: "workflow", TargetRef: definition.Name,
		WorkflowRunID: sql.NullString{String: waiting.RunID, Valid: true}, State: a2a.TaskStateWorking,
	}); createErr != nil {
		t.Fatal(createErr)
	}

	task, err := tm.GetTask(t.Context(), taskID)
	if err != nil || task.State != a2a.TaskStateInputRequired {
		t.Fatalf("waiting task = %+v, %v", task, err)
	}
	if provideErr := tm.ProvideTaskInput(t.Context(), taskID, "approved"); provideErr != nil {
		t.Fatalf("ProvideTaskInput: %v", provideErr)
	}
	task, err = tm.GetTask(t.Context(), taskID)
	if err != nil || task.State != a2a.TaskStateCompleted {
		t.Fatalf("completed task = %+v, %v", task, err)
	}
	run, err := st.GetWorkflowRun(t.Context(), waiting.RunID)
	if err != nil || run == nil || run.DefinitionName != definition.Name || run.Status != "completed" {
		t.Fatalf("product run = %+v, %v", run, err)
	}
	steps, err := st.ListWorkflowRunSteps(t.Context(), waiting.RunID)
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]*store.WorkflowRunStepRow, len(steps))
	for _, step := range steps {
		byID[step.StepID] = step
	}
	if gate := byID["approve release"]; gate == nil || gate.Status != "completed" || gate.GateInput != "approved" {
		t.Fatalf("gate projection = %+v", gate)
	}
	if len(executor.tools) != 1 {
		t.Fatalf("tool calls = %+v", executor.tools)
	}
	tool := executor.tools[0]
	if tool.WorkflowRunID != waiting.RunID || tool.SessionID != "a2a-workflow-session" || tool.AgentID != "release-agent" || tool.Args["approval"] != "approved" {
		t.Fatalf("tool identity = %+v", tool)
	}
}

type a2aHadronStepExecutor struct {
	tools []agentworkflow.ToolStepRequest
}

func (*a2aHadronStepExecutor) ExecuteLLMStep(context.Context, agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error) {
	return agentworkflow.LLMStepResult{}, nil
}

func (e *a2aHadronStepExecutor) ExecuteToolStep(_ context.Context, request agentworkflow.ToolStepRequest) (agentworkflow.ToolStepResult, error) {
	e.tools = append(e.tools, request)
	return agentworkflow.ToolStepResult{Output: "published"}, nil
}

func (*a2aHadronStepExecutor) Verify(context.Context, agentworkflow.VerifyRequest) (agentworkflow.VerifyResult, error) {
	return agentworkflow.VerifyResult{Passed: true}, nil
}

// TestDeriveFromWorkflowRun_WaitingOnGate verifies that a workflow run in
// waiting_on_gate status correctly derives to TaskStateInputRequired.
func TestDeriveFromWorkflowRun_WaitingOnGate(t *testing.T) {
	st := newTestStore(t)
	defer st.Close(context.

		// Create a workflow run in waiting_on_gate status.
		Background())

	runID := "run-gate-test"
	if err := st.CreateWorkflowRun(context.Background(), &store.WorkflowRunRow{
		ID:             runID,
		DefinitionName: "test-workflow",
		Status:         "waiting_on_gate",
	}); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}

	// Create a task pointing to this run.
	task := &store.A2ATask{
		ID:            "task-gate-test",
		TargetKind:    "workflow",
		TargetRef:     "test-workflow",
		WorkflowRunID: sql.NullString{String: runID, Valid: true},
		State:         a2a.TaskStateWorking, // Cached state, will be re-derived
	}
	if err := st.CreateA2ATask(context.Background(), task); err != nil {
		t.Fatalf("CreateA2ATask: %v", err)
	}

	// Create a TaskManager and derive state.
	tm := &TaskManager{
		store:    st,
		registry: agentworkflow.NewRegistry(nil),
		logger:   testLogger(t),
	}

	derivedState := tm.deriveFromWorkflowRun(context.Background(), runID)

	if derivedState != a2a.TaskStateInputRequired {
		t.Errorf("derived state = %q, want %q", derivedState, a2a.TaskStateInputRequired)
	}
}

// TestResolveGate_UpdatesStepStatus verifies that ResolveGate transitions a
// waiting gate to completed and stores the input.
func TestResolveGate_UpdatesStepStatus(t *testing.T) {
	st := newTestStore(t)
	defer st.Close(context.Background())

	runID := "run-resolve-test"
	stepID := "gate1"

	// Create a workflow run.
	if err := st.CreateWorkflowRun(context.Background(), &store.WorkflowRunRow{
		ID:             runID,
		DefinitionName: "test-workflow",
		Status:         "running",
	}); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}

	// Create a gate step in waiting_on_gate status.
	if err := st.UpsertWorkflowRunStep(context.Background(), &store.WorkflowRunStepRow{
		WorkflowRunID: runID,
		StepID:        stepID,
		Kind:          "gate",
		Status:        "waiting_on_gate",
	}); err != nil {
		t.Fatalf("UpsertWorkflowRunStep: %v", err)
	}

	// Resolve the gate with input.
	input := "user approved: proceed"
	if err := st.ResolveGate(context.Background(), runID, stepID, input); err != nil {
		t.Fatalf("ResolveGate: %v", err)
	}

	// Verify the step is now completed with the input stored.
	steps, err := st.ListWorkflowRunSteps(context.Background(), runID)
	if err != nil {
		t.Fatalf("ListWorkflowRunSteps: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(steps))
	}

	step := steps[0]
	if step.Status != "completed" {
		t.Errorf("step.Status = %q, want %q", step.Status, "completed")
	}
	if step.GateInput != input {
		t.Errorf("step.GateInput = %q, want %q", step.GateInput, input)
	}
	if step.Output != "Gate resolved: "+input {
		t.Errorf("step.Output = %q, want %q", step.Output, "Gate resolved: "+input)
	}
}

// TestGetWaitingGates_ReturnsOnlyWaitingGates verifies GetWaitingGates filters
// correctly.
func TestGetWaitingGates_ReturnsOnlyWaitingGates(t *testing.T) {
	st := newTestStore(t)
	defer st.Close(context.Background())

	runID := "run-waiting-gates-test"

	// Create a workflow run.
	if err := st.CreateWorkflowRun(context.Background(), &store.WorkflowRunRow{
		ID:             runID,
		DefinitionName: "test-workflow",
		Status:         "running",
	}); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}

	// Create multiple steps: one waiting gate, one completed gate, one non-gate.
	steps := []store.WorkflowRunStepRow{
		{WorkflowRunID: runID, StepID: "gate1", Kind: "gate", Status: "waiting_on_gate"},
		{WorkflowRunID: runID, StepID: "gate2", Kind: "gate", Status: "completed"},
		{WorkflowRunID: runID, StepID: "step1", Kind: "llm", Status: "completed"},
	}

	for _, step := range steps {
		if err := st.UpsertWorkflowRunStep(context.Background(), &step); err != nil {
			t.Fatalf("UpsertWorkflowRunStep(%s): %v", step.StepID, err)
		}
	}

	// Get waiting gates.
	waiting, err := st.GetWaitingGates(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetWaitingGates: %v", err)
	}

	if len(waiting) != 1 {
		t.Fatalf("got %d waiting gates, want 1", len(waiting))
	}
	if waiting[0].StepID != "gate1" {
		t.Errorf("waiting[0].StepID = %q, want %q", waiting[0].StepID, "gate1")
	}
}

// Helper to create a test store with the schema applied.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := storetest.New(t, context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	return st
}

// Helper to create a test logger.
func testLogger(t *testing.T) *slog.Logger {
	return slog.Default()
}
