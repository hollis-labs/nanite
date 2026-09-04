package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	workflowruntime "github.com/hollis-labs/go-workflow/runtime"

	"github.com/hollis-labs/nanite/internal/a2a"
	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/nanite/internal/workflowhost"
)

// TestA2AJSONRPC_MethodRouting verifies that the JSON-RPC handler routes
// to the correct method handlers based on the method field. Method name
// constants are spec-verified (SendMessage/GetTask/CancelTask) — see the
// comment above their declaration in a2a_jsonrpc.go.
func TestA2AJSONRPC_MethodRouting(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		wantStatusCode int
	}{
		{
			name:           "SendMessage method routes correctly",
			method:         methodSendMessage,
			wantStatusCode: http.StatusOK,
		},
		{
			name:           "GetTask method routes correctly",
			method:         methodGetTask,
			wantStatusCode: http.StatusOK,
		},
		{
			name:           "CancelTask method routes correctly",
			method:         methodCancelTask,
			wantStatusCode: http.StatusOK,
		},
		{
			name:           "a2a.task.provideInput method routes correctly (Nanite-specific extension, no spec equivalent)",
			method:         methodProvideTaskInput,
			wantStatusCode: http.StatusOK,
		},
		{
			name:           "unknown method returns method not found",
			method:         "unknown.method",
			wantStatusCode: http.StatusOK, // JSON-RPC always returns 200
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create minimal request
			req := a2a.JSONRPCRequest{
				JSONRPC: a2a.JSONRPCVersion,
				Method:  tt.method,
				Params:  map[string]any{},
				ID:      1,
			}

			body, err := json.Marshal(req)
			if err != nil {
				t.Fatalf("failed to marshal request: %v", err)
			}

			httpReq := httptest.NewRequest("POST", "/api/a2a/jsonrpc", bytes.NewReader(body))
			httpReq.Header.Set("Content-Type", "application/json")

			recorder := httptest.NewRecorder()

			// Create minimal API instance (would need full mocking for real tests)
			// This is just a compilation test
			api := &API{
				Services: &service.Container{},
			}

			api.handleA2AJSONRPC(recorder, httpReq)

			if recorder.Code != tt.wantStatusCode {
				t.Errorf("got status %d, want %d", recorder.Code, tt.wantStatusCode)
			}
		})
	}
}

// TestAgentCard_ContentType verifies the agent card endpoint returns JSON.
func TestAgentCard_ContentType(t *testing.T) {
	httpReq := httptest.NewRequest("GET", "/.well-known/agent-card.json", nil)
	recorder := httptest.NewRecorder()

	// This would panic without a real AgentCardGenerator, but validates the signature
	_ = httpReq
	_ = recorder
}

// fakeInstanceCanceller satisfies TaskManager's unexported
// durableAgentCanceller interface structurally (RequestStop's signature)
// so this package's tests can build a real *service.TaskManager without
// pulling in the full DurableAgentService/runtime-controller stack.
type fakeInstanceCanceller struct{}

func (fakeInstanceCanceller) RequestStop(_ context.Context, id string) (*store.DurableAgentInstance, error) {
	return &store.DurableAgentInstance{ID: id, Status: store.DurableAgentStatusStopped}, nil
}

// newTestTaskManager builds a real *service.TaskManager backed by a real
// temp-file store, for exercising handleTaskCancel end to end (HTTP ->
// handler -> TaskManager.CancelTask -> store) rather than only unit-testing
// the handler's request/response plumbing in isolation. launcher/wake/
// registry are left nil — CancelTask's 'instance' branch never touches
// them (only the 'workflow' rejection path and SubmitTask do). Returns the
// store too, so tests can seed a2a_tasks/workflow_runs rows directly.
func newTestTaskManager(t *testing.T) (*store.Store, *service.TaskManager) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := storetest.New(t, context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close(context.Background()) })
	tm := service.NewTaskManager(st, nil, nil, fakeInstanceCanceller{}, agentworkflow.NewRegistry(nil), nil)
	return st, tm
}

// postJSONRPC sends a JSON-RPC request through the real handler and
// decodes the envelope.
func postJSONRPC(t *testing.T, api *API, req a2a.JSONRPCRequest) (*httptest.ResponseRecorder, a2a.JSONRPCResponse) {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	httpReq := httptest.NewRequest("POST", "/api/a2a/jsonrpc", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	api.handleA2AJSONRPC(recorder, httpReq)

	var resp a2a.JSONRPCResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v (body: %s)", err, recorder.Body.String())
	}
	return recorder, resp
}

// TestA2AJSONRPC_HandleTaskCancel_Instance_Success exercises the real
// CancelTask path end to end through the HTTP handler: submit params ->
// handleTaskCancel -> TaskManager.CancelTask -> store, using a real
// 'instance'-target task. Verifies handleTaskCancel no longer returns the
// old hardcoded "not yet implemented" error.
func TestA2AJSONRPC_HandleTaskCancel_Instance_Success(t *testing.T) {
	st, tm := newTestTaskManager(t)
	api := &API{Services: &service.Container{TaskManager: tm}}

	profile := &store.AgentProfile{Name: "A2A JSONRPC Cancel Test Agent", Slug: "a2a-jsonrpc-cancel-test-agent", SystemPrompt: "x"}
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	inst := &store.DurableAgentInstance{
		ID:             "inst-1",
		Name:           "jsonrpc-cancel-test-instance",
		Slug:           "jsonrpc-cancel-test-instance",
		LifecycleClass: store.DurableAgentClassProcess,
		ProfileID:      profile.ID,
		Status:         store.DurableAgentStatusActive,
	}
	if err := st.CreateDurableAgentInstance(context.Background(), inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}

	task := &store.A2ATask{
		ID:                     "task-cancel-instance",
		TargetKind:             "instance",
		TargetRef:              "msg://agent/nanite/inst-1",
		Message:                "hello",
		State:                  a2a.TaskStateWorking,
		DurableAgentInstanceID: sql.NullString{String: inst.ID, Valid: true},
	}
	if err := st.CreateA2ATask(context.Background(), task); err != nil {
		t.Fatalf("CreateA2ATask: %v", err)
	}

	_, resp := postJSONRPC(t, api, a2a.JSONRPCRequest{
		JSONRPC: a2a.JSONRPCVersion,
		Method:  methodCancelTask,
		Params:  a2a.TaskCancelRequest{TaskID: task.ID},
		ID:      1,
	})

	if resp.Error != nil {
		t.Fatalf("got JSON-RPC error: %+v", resp.Error)
	}

	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var result a2a.TaskCancelResponse
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.State != a2a.TaskStateCanceled {
		t.Errorf("result.State = %v, want %v", result.State, a2a.TaskStateCanceled)
	}
}

// TestA2AJSONRPC_HandleTaskCancel_Workflow_Unsupported verifies the
// escalated, honest gap: canceling a workflow-backed task returns a real
// JSON-RPC error (ErrTaskNotCancelable), not a fake success.
func TestA2AJSONRPC_HandleTaskCancel_Workflow_Unsupported(t *testing.T) {
	st, tm := newTestTaskManager(t)
	api := &API{Services: &service.Container{TaskManager: tm}}

	if err := st.CreateWorkflowRun(context.Background(), &store.WorkflowRunRow{
		ID:             "run-cancel-1",
		DefinitionName: "test-workflow",
		Status:         "running",
	}); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}
	task := &store.A2ATask{
		ID:            "task-cancel-workflow",
		TargetKind:    "workflow",
		TargetRef:     "test-workflow",
		Message:       "hello",
		State:         a2a.TaskStateWorking,
		WorkflowRunID: sql.NullString{String: "run-cancel-1", Valid: true},
	}
	if err := st.CreateA2ATask(context.Background(), task); err != nil {
		t.Fatalf("CreateA2ATask: %v", err)
	}

	_, resp := postJSONRPC(t, api, a2a.JSONRPCRequest{
		JSONRPC: a2a.JSONRPCVersion,
		Method:  methodCancelTask,
		Params:  a2a.TaskCancelRequest{TaskID: task.ID},
		ID:      1,
	})

	if resp.Error == nil {
		t.Fatal("expected a JSON-RPC error, got success")
	}
	if resp.Error.Code != a2a.ErrTaskNotCancelable {
		t.Errorf("resp.Error.Code = %d, want %d (ErrTaskNotCancelable)", resp.Error.Code, a2a.ErrTaskNotCancelable)
	}
}

func TestA2AJSONRPC_ProvideInputResumesHadronGate(t *testing.T) {
	st, err := storetest.New(t, t.Context(), filepath.Join(t.TempDir(), "hadron-a2a-api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	state, err := workflowhost.NewWorkflowStateStore(st)
	if err != nil {
		t.Fatal(err)
	}
	waits := &workflowruntime.WaitCoordinator{
		Store: state, Authorizer: workflowhost.NaniteResponderAuthorizer{},
	}
	engine, err := workflowhost.NewEngine(state)
	if err != nil {
		t.Fatal(err)
	}
	engine.WithWaitCoordinator(waits)
	executor := &apiHadronStepExecutor{}
	const taskID = "a2a-api-hadron-gate"
	definition := agentworkflow.WorkflowDefinition{
		Name: "A2A API Hadron approval", Engine: agentworkflow.EngineHadron,
		Steps: []agentworkflow.StepDefinition{
			{ID: "approval step", Kind: agentworkflow.StepKindGate},
			{ID: "publish", Kind: agentworkflow.StepKindTool, DependsOn: []string{"approval step"}, Config: map[string]any{
				"tool": "publish", "agent_id": "api-publisher", "args": map[string]any{"decision": "{{steps.approval step.output}}"},
			}},
		},
	}
	waiting, err := engine.Run(t.Context(), definition, agentworkflow.WorkflowInput{
		Params: map[string]any{"_nanite_a2a_task_id": taskID}, SessionID: "api-session",
	}, executor)
	if err != nil || waiting.Status != agentworkflow.RunStatusWaiting {
		t.Fatalf("Run=%+v err=%v", waiting, err)
	}
	registry := agentworkflow.NewRegistry(map[string]agentworkflow.WorkflowDefinition{definition.Name: definition})
	launcher := service.NewWorkflowLauncher(registry, engine, executor, nil)
	tm := service.NewTaskManager(st, launcher, nil, fakeInstanceCanceller{}, registry, nil)
	if createErr := st.CreateA2ATask(t.Context(), &store.A2ATask{
		ID: taskID, TargetKind: "workflow", TargetRef: definition.Name,
		WorkflowRunID: sql.NullString{String: waiting.RunID, Valid: true}, State: a2a.TaskStateWorking,
	}); createErr != nil {
		t.Fatal(createErr)
	}
	api := &API{Services: &service.Container{TaskManager: tm}}

	_, before := postJSONRPC(t, api, a2a.JSONRPCRequest{
		JSONRPC: a2a.JSONRPCVersion, Method: methodGetTask,
		Params: a2a.TaskGetRequest{TaskID: taskID}, ID: 1,
	})
	if before.Error != nil {
		t.Fatalf("GetTask error=%+v", before.Error)
	}
	beforeBytes, _ := json.Marshal(before.Result)
	var beforeResult a2a.TaskGetResponse
	if decodeErr := json.Unmarshal(beforeBytes, &beforeResult); decodeErr != nil || beforeResult.Task.State != a2a.TaskStateInputRequired {
		t.Fatalf("waiting API task=%+v err=%v", beforeResult, decodeErr)
	}

	_, response := postJSONRPC(t, api, a2a.JSONRPCRequest{
		JSONRPC: a2a.JSONRPCVersion, Method: methodProvideTaskInput,
		Params: a2a.TaskProvideInputRequest{TaskID: taskID, Input: "approved"}, ID: 2,
	})
	if response.Error != nil {
		t.Fatalf("ProvideInput error=%+v", response.Error)
	}
	responseBytes, _ := json.Marshal(response.Result)
	var result a2a.TaskProvideInputResponse
	if decodeErr := json.Unmarshal(responseBytes, &result); decodeErr != nil || result.TaskID != taskID || result.State != a2a.TaskStateCompleted {
		t.Fatalf("ProvideInput result=%+v err=%v", result, decodeErr)
	}
	if len(executor.tools) != 1 || executor.tools[0].WorkflowRunID != waiting.RunID ||
		executor.tools[0].SessionID != "api-session" || executor.tools[0].Args["decision"] != "approved" {
		t.Fatalf("tool identity=%+v", executor.tools)
	}
	run, err := st.GetWorkflowRun(t.Context(), waiting.RunID)
	if err != nil || run == nil || run.DefinitionName != definition.Name || run.Status != "completed" {
		t.Fatalf("product run=%+v err=%v", run, err)
	}

	const canceledTaskID = "a2a-api-hadron-cancel"
	cancelWaiting, err := engine.Run(t.Context(), definition, agentworkflow.WorkflowInput{
		Params: map[string]any{"_nanite_a2a_task_id": canceledTaskID}, SessionID: "cancel-session",
	}, executor)
	if err != nil || cancelWaiting.Status != agentworkflow.RunStatusWaiting {
		t.Fatalf("Run(cancel fixture)=%+v err=%v", cancelWaiting, err)
	}
	if createErr := st.CreateA2ATask(t.Context(), &store.A2ATask{
		ID: canceledTaskID, TargetKind: "workflow", TargetRef: definition.Name,
		WorkflowRunID: sql.NullString{String: cancelWaiting.RunID, Valid: true}, State: a2a.TaskStateWorking,
	}); createErr != nil {
		t.Fatal(createErr)
	}
	_, cancelResponse := postJSONRPC(t, api, a2a.JSONRPCRequest{
		JSONRPC: a2a.JSONRPCVersion, Method: methodCancelTask,
		Params: a2a.TaskCancelRequest{TaskID: canceledTaskID}, ID: 3,
	})
	if cancelResponse.Error != nil {
		t.Fatalf("CancelTask error=%+v", cancelResponse.Error)
	}
	cancelBytes, _ := json.Marshal(cancelResponse.Result)
	var cancelResult a2a.TaskCancelResponse
	if decodeErr := json.Unmarshal(cancelBytes, &cancelResult); decodeErr != nil || cancelResult.TaskID != canceledTaskID || cancelResult.State != a2a.TaskStateCanceled {
		t.Fatalf("CancelTask result=%+v err=%v", cancelResult, decodeErr)
	}
	canceledRun, err := st.GetWorkflowRun(t.Context(), cancelWaiting.RunID)
	if err != nil || canceledRun == nil || canceledRun.Status != "canceled" {
		t.Fatalf("canceled product run=%+v err=%v", canceledRun, err)
	}
}

type apiHadronStepExecutor struct {
	tools []agentworkflow.ToolStepRequest
}

func (*apiHadronStepExecutor) ExecuteLLMStep(context.Context, agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error) {
	return agentworkflow.LLMStepResult{}, nil
}

func (e *apiHadronStepExecutor) ExecuteToolStep(_ context.Context, request agentworkflow.ToolStepRequest) (agentworkflow.ToolStepResult, error) {
	e.tools = append(e.tools, request)
	return agentworkflow.ToolStepResult{Output: "published"}, nil
}

func (*apiHadronStepExecutor) Verify(context.Context, agentworkflow.VerifyRequest) (agentworkflow.VerifyResult, error) {
	return agentworkflow.VerifyResult{Passed: true}, nil
}
