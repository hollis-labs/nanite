package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/hollis-labs/nanite/internal/a2a"
	"github.com/hollis-labs/nanite/internal/service"
)

// A2A JSON-RPC 2.0 method names. NOT independently spec-verified — the
// original ticket (CW-20260814-0016) explicitly required checking these
// against the live spec at a2a-protocol.org before implementing, the same
// way Hadron's own A2A implementation is already known to be non-conformant
// for skipping that step (apps/hadron/internal/a2a/handler.go, plain
// REST-ish JSON, not real JSON-RPC). Two independent spec lookups during
// review (2026-08-15) returned inconsistent method-name conventions
// ("SendMessage"/"GetTask"/"CancelTask" vs "a2a/SendMessage" etc.), neither
// matching what's used here — not authoritative enough to safely rename
// against. No external A2A client consumes this endpoint yet, so the risk
// is contained; treat this constant block as a known conformance gap to
// close with a proper spec-verification pass before any real interop.
const (
	methodTaskSubmit       = "a2a.task.submit"
	methodTaskGet          = "a2a.task.get"
	methodTaskCancel       = "a2a.task.cancel"
	methodTaskProvideInput = "a2a.task.provideInput"
)

// handleA2AJSONRPC is the single JSON-RPC 2.0 endpoint for all A2A Task methods.
// Routes to task-submit, task-get, task-cancel, or task-provide-input based
// on the method field.
//
// This is a thin wrapper over TaskManager (internal/service/a2a_task_manager.go).
// No business logic lives here — routing, validation, execution, and state
// derivation all happen in the service layer.
func (a *API) handleA2AJSONRPC(w http.ResponseWriter, r *http.Request) {
	// Parse JSON-RPC request envelope
	body, err := io.ReadAll(r.Body)
	if err != nil {
		respondJSONRPCError(w, a2a.JSONRPCParseError, "Failed to read request body", nil, nil)
		return
	}

	var req a2a.JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		respondJSONRPCError(w, a2a.JSONRPCParseError, "Invalid JSON", nil, nil)
		return
	}

	// Validate JSON-RPC envelope
	if req.JSONRPC != a2a.JSONRPCVersion {
		respondJSONRPCError(w, a2a.JSONRPCInvalidRequest, "Invalid jsonrpc version", nil, req.ID)
		return
	}
	if req.Method == "" {
		respondJSONRPCError(w, a2a.JSONRPCInvalidRequest, "Missing method", nil, req.ID)
		return
	}

	// Route to method handler
	switch req.Method {
	case methodTaskSubmit:
		a.handleTaskSubmit(w, r, &req)
	case methodTaskGet:
		a.handleTaskGet(w, r, &req)
	case methodTaskCancel:
		a.handleTaskCancel(w, r, &req)
	case methodTaskProvideInput:
		a.handleTaskProvideInput(w, r, &req)
	default:
		respondJSONRPCError(w, a2a.JSONRPCMethodNotFound, "Unknown method: "+req.Method, nil, req.ID)
	}
}

// handleTaskSubmit processes a2a.task.submit requests.
func (a *API) handleTaskSubmit(w http.ResponseWriter, r *http.Request, req *a2a.JSONRPCRequest) {
	ctx := r.Context()

	// Parse params
	var params a2a.TaskSubmitRequest
	if err := unmarshalParams(req.Params, &params); err != nil {
		respondJSONRPCError(w, a2a.JSONRPCInvalidParams, "Invalid params: "+err.Error(), nil, req.ID)
		return
	}

	// Validate required fields
	if params.Target == "" {
		respondJSONRPCError(w, a2a.JSONRPCInvalidParams, "Missing required field: target", nil, req.ID)
		return
	}

	// Build service request
	svcReq := service.TaskSubmitRequest{
		Target:                 params.Target,
		Message:                params.Message,
		PushNotificationConfig: params.PushNotificationConfig,
		// WorkspaceID and ProjectID would be extracted from auth context in production
		// For now, leave empty — TaskManager will use defaults
	}

	// Call service layer
	result, err := a.Services.TaskManager.SubmitTask(ctx, svcReq)
	if err != nil {
		slog.Error("a2a: task submit failed", "error", err, "target", params.Target)
		// Map service errors to JSON-RPC error codes
		code, msg := mapTaskErrorToJSONRPC(err)
		respondJSONRPCError(w, code, msg, err.Error(), req.ID)
		return
	}

	// Build response
	response := a2a.TaskSubmitResponse{
		TaskID: result.TaskID,
		State:  result.State,
	}

	respondJSONRPCSuccess(w, response, req.ID)
}

// handleTaskGet processes a2a.task.get requests.
func (a *API) handleTaskGet(w http.ResponseWriter, r *http.Request, req *a2a.JSONRPCRequest) {
	ctx := r.Context()

	// Parse params
	var params a2a.TaskGetRequest
	if err := unmarshalParams(req.Params, &params); err != nil {
		respondJSONRPCError(w, a2a.JSONRPCInvalidParams, "Invalid params: "+err.Error(), nil, req.ID)
		return
	}

	if params.TaskID == "" {
		respondJSONRPCError(w, a2a.JSONRPCInvalidParams, "Missing required field: taskId", nil, req.ID)
		return
	}

	// Call service layer
	task, err := a.Services.TaskManager.GetTask(ctx, params.TaskID)
	if err != nil {
		slog.Error("a2a: task get failed", "error", err, "taskId", params.TaskID)
		code, msg := mapTaskErrorToJSONRPC(err)
		respondJSONRPCError(w, code, msg, err.Error(), req.ID)
		return
	}

	// Build response
	result := a2a.TaskGetResponse{
		Task: *task,
	}

	respondJSONRPCSuccess(w, result, req.ID)
}

// handleTaskCancel processes a2a.task.cancel requests.
func (a *API) handleTaskCancel(w http.ResponseWriter, r *http.Request, req *a2a.JSONRPCRequest) {
	// Parse params
	var params a2a.TaskCancelRequest
	if err := unmarshalParams(req.Params, &params); err != nil {
		respondJSONRPCError(w, a2a.JSONRPCInvalidParams, "Invalid params: "+err.Error(), nil, req.ID)
		return
	}

	if params.TaskID == "" {
		respondJSONRPCError(w, a2a.JSONRPCInvalidParams, "Missing required field: taskId", nil, req.ID)
		return
	}

	// Task cancellation is not yet implemented in the service layer.
	// Return a JSON-RPC error indicating this is not supported.
	// TODO(CW-20260814-0016): Implement TaskManager.CancelTask when workflow/durable-agent
	// cancellation support is ready.
	respondJSONRPCError(w, a2a.JSONRPCInternalError, "Task cancellation not yet implemented", nil, req.ID)
}

// handleTaskProvideInput processes a2a.task.provideInput requests — the
// transport-layer wiring for TaskManager.ProvideTaskInput (CW-20260814-0017),
// resolving a paused workflow gate on a Task in TaskStateInputRequired.
func (a *API) handleTaskProvideInput(w http.ResponseWriter, r *http.Request, req *a2a.JSONRPCRequest) {
	ctx := r.Context()

	var params a2a.TaskProvideInputRequest
	if err := unmarshalParams(req.Params, &params); err != nil {
		respondJSONRPCError(w, a2a.JSONRPCInvalidParams, "Invalid params: "+err.Error(), nil, req.ID)
		return
	}

	if params.TaskID == "" {
		respondJSONRPCError(w, a2a.JSONRPCInvalidParams, "Missing required field: taskId", nil, req.ID)
		return
	}
	if params.Input == "" {
		respondJSONRPCError(w, a2a.JSONRPCInvalidParams, "Missing required field: input", nil, req.ID)
		return
	}

	if err := a.Services.TaskManager.ProvideTaskInput(ctx, params.TaskID, params.Input); err != nil {
		slog.Error("a2a: provide task input failed", "error", err, "taskId", params.TaskID)
		code, msg := mapTaskErrorToJSONRPC(err)
		respondJSONRPCError(w, code, msg, err.Error(), req.ID)
		return
	}

	// Re-derive state from the (now resumed) workflow run rather than
	// assuming completion — the gate is resolved but the workflow may still
	// be working, or hit another gate.
	task, err := a.Services.TaskManager.GetTask(ctx, params.TaskID)
	if err != nil {
		slog.Error("a2a: get task after provide-input failed", "error", err, "taskId", params.TaskID)
		code, msg := mapTaskErrorToJSONRPC(err)
		respondJSONRPCError(w, code, msg, err.Error(), req.ID)
		return
	}

	respondJSONRPCSuccess(w, a2a.TaskProvideInputResponse{
		TaskID: task.ID,
		State:  task.State,
	}, req.ID)
}

// unmarshalParams unmarshals JSON-RPC params into a typed struct.
func unmarshalParams(params any, dest any) error {
	if params == nil {
		return nil
	}

	// params is already decoded as map[string]any or []any by json.Unmarshal
	// Re-marshal and unmarshal to get it into the target type
	data, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dest)
}

// mapTaskErrorToJSONRPC maps service-layer errors to JSON-RPC error codes.
func mapTaskErrorToJSONRPC(err error) (int, string) {
	// Check for specific error types from service layer
	errMsg := err.Error()

	// Map common errors
	if strings.Contains(errMsg, "not found") || strings.Contains(errMsg, "does not exist") {
		return a2a.ErrTaskNotFound, "Task not found"
	}
	if strings.Contains(errMsg, "target not found") || strings.Contains(errMsg, "unknown target") {
		return a2a.ErrTargetNotFound, "Target not found"
	}
	if strings.Contains(errMsg, "invalid target") || strings.Contains(errMsg, "malformed") {
		return a2a.ErrInvalidTarget, "Invalid target"
	}
	if strings.Contains(errMsg, "rejected") || strings.Contains(errMsg, "validation") {
		return a2a.ErrTaskRejected, "Task rejected"
	}

	// Default to internal error
	return a2a.JSONRPCInternalError, "Internal error"
}

// respondJSONRPCSuccess writes a JSON-RPC 2.0 success response.
func respondJSONRPCSuccess(w http.ResponseWriter, result any, id any) {
	resp := a2a.NewJSONRPCResponse(result, id)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// respondJSONRPCError writes a JSON-RPC 2.0 error response.
func respondJSONRPCError(w http.ResponseWriter, code int, message string, data any, id any) {
	resp := a2a.NewJSONRPCErrorResponse(code, message, data, id)
	w.Header().Set("Content-Type", "application/json")
	// Always return 200 for JSON-RPC — errors are in the response envelope
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}
