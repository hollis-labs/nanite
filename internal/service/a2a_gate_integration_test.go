package service

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"

	workflowruntime "github.com/hollis-labs/libs/workflow/runtime"

	"github.com/hollis-labs/nanite/internal/a2a"
	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/nanite/internal/workflowhost"
)

func TestA2AGateIntegrationRequiresVerifiedAuthority(t *testing.T) {
	st := newTestStore(t)
	defer st.Close(context.Background())
	before := a2aRefusalState(t, st)
	task := &store.A2ATask{ID: "claimed-gate-task", TargetKind: "workflow", TargetRef: "claimed-workflow", State: a2a.TaskStateWorking}
	if err := st.CreateA2ATask(t.Context(), task); !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("task create = %v", err)
	}
	tm := &TaskManager{store: st, logger: testLogger(t)}
	if result, err := tm.GetTask(t.Context(), task.ID); result != nil || !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("GetTask = %+v %v", result, err)
	}
	if err := tm.ProvideTaskInput(t.Context(), task.ID, "claimed approval"); !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("ProvideTaskInput = %v", err)
	}
	assertA2ARefusalState(t, st, before)
}

func TestA2AHadronGateInputRefusesWithoutTaskAuthority(t *testing.T) {
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
	// Retain only historical correlation in this private DB; it is not an
	// authenticated responder identity and cannot authorize a gate resume.
	historicalA2ATask(t, st, &store.A2ATask{ID: taskID, TargetKind: "workflow", TargetRef: definition.Name, WorkflowRunID: sql.NullString{String: waiting.RunID, Valid: true}, State: a2a.TaskStateWorking})
	before := a2aRefusalState(t, st)
	if task, getErr := tm.GetTask(t.Context(), taskID); task != nil || !errors.Is(getErr, store.ErrVerifiedActorRequired) {
		t.Fatalf("GetTask = %+v %v", task, getErr)
	}
	if inputErr := tm.ProvideTaskInput(t.Context(), taskID, "claimed approval"); !errors.Is(inputErr, store.ErrVerifiedActorRequired) {
		t.Fatalf("ProvideTaskInput = %v", inputErr)
	}
	if task, cancelErr := tm.CancelTask(t.Context(), taskID); task != nil || !errors.Is(cancelErr, store.ErrVerifiedActorRequired) {
		t.Fatalf("CancelTask = %+v %v", task, cancelErr)
	}
	if len(executor.tools) != 0 {
		t.Fatalf("held input executed tools: %+v", executor.tools)
	}
	assertA2ARefusalState(t, st, before)

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

	// Workflow-state classification needs no issued A2A task or actor.
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
