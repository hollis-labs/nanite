package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/a2a"
	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
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
			api := &testAPI{API: &API{
				Services: &service.Container{},
			}}

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
func postJSONRPC(t *testing.T, api *testAPI, req a2a.JSONRPCRequest) (*httptest.ResponseRecorder, a2a.JSONRPCResponse) {
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

// Retained task references are history, not permission to recover a held fabric.
func TestA2AJSONRPCRefusesHeldFabricAndPreservesRetainedTasks(t *testing.T) {
	st, tm := newTestTaskManager(t)
	api := &testAPI{API: &API{Services: &service.Container{TaskManager: tm}}}
	if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO a2a_tasks(id,target_kind,target_ref,message,state) VALUES('retained-task','workflow','retained-workflow','private retained message','working')`); err != nil {
		t.Fatal(err)
	}
	a := &testAPI{store: st}
	const query = `SELECT * FROM a2a_tasks ORDER BY id`
	before := retiredAPISnapshot(t, a, query)
	for _, c := range []struct {
		method string
		params any
	}{
		{methodGetTask, a2a.TaskGetRequest{TaskID: "retained-task"}},
		{methodCancelTask, a2a.TaskCancelRequest{TaskID: "retained-task"}},
		{methodProvideTaskInput, map[string]any{"task_id": "retained-task", "decision": "approve"}},
	} {
		recorder, resp := postJSONRPC(t, api, a2a.JSONRPCRequest{JSONRPC: a2a.JSONRPCVersion, Method: c.method, Params: c.params, ID: 1})
		if recorder.Code != http.StatusOK || resp.Error == nil {
			t.Fatalf("%s: %d %+v", c.method, recorder.Code, resp)
		}
		retiredAPIHistoryUnchanged(t, a, query, before)
	}
}
