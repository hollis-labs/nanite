package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/hollis-labs/nanite/internal/a2a"
	"github.com/hollis-labs/nanite/internal/service"
)

// A2A JSON-RPC 2.0 method names — spec-verified 2026-08-18 (CW-20260814-0016
// conformance close-out; see TASKS/phase-0/08-a2a-conformance.md's Work log
// for the full citation trail). Source: the official A2A Protocol
// specification, github.com/a2aproject/A2A, release tag v1.0.1 (published
// 2026-05-26, the current latest stable release, mirrored at
// https://a2a-protocol.org/latest/specification/):
//   - docs/specification.md §5.3 "Method Mapping Reference" — the
//     JSON-RPC-column values are bare PascalCase, no prefix/namespace:
//     SendMessage, GetTask, CancelTask (also ListTasks, SubscribeToTask,
//     the four TaskPushNotificationConfig methods, and
//     GetExtendedAgentCard, none of which Nanite implements yet).
//   - docs/specification.md §9.4 "Core Methods" gives literal example
//     request bodies confirming the wire value directly, e.g.
//     `"method": "SendMessage"` — not "a2a/SendMessage" or "message/send".
//
// Historical note on the earlier "two inconsistent lookups" (the comment
// this replaced, from the 2026-08-15 review): pre-1.0 drafts (tag v0.3.0,
// specification/json/a2a.json) used slash-style names (message/send,
// tasks/get, tasks/cancel). v1.0.0 (released 2026-03-12, five months
// before that review) renamed the whole method set to bare PascalCase "for
// consistency and clarity" across the REST/gRPC/JSON-RPC bindings
// (docs/whats-new-v1.md, "RENAMED" entries per method). The 2026-08-15
// review's two "inconsistent" results were almost certainly one stale
// pre-1.0 snapshot and one current one, not genuine live ambiguity — the
// spec has been stable and singular on PascalCase since March 2026.
//
// methodProvideTaskInput has no spec equivalent and stays intentionally
// non-spec-shaped: the spec's "Input Required State" section says a paused
// task resumes via a new SendMessage carrying the same taskId/contextId,
// not a dedicated method. This is a legitimate Nanite-specific extension
// (CW-20260814-0017, resolving a paused workflow gate), not force-fit into
// spec vocabulary.
const (
	methodSendMessage      = "SendMessage"
	methodGetTask          = "GetTask"
	methodCancelTask       = "CancelTask"
	methodProvideTaskInput = "a2a.task.provideInput"
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
	case methodSendMessage:
		a.handleTaskSubmit(w, r, &req)
	case methodGetTask:
		a.handleTaskGet(w, r, &req)
	case methodCancelTask:
		a.handleTaskCancel(w, r, &req)
	case methodProvideTaskInput:
		a.handleTaskProvideInput(w, r, &req)
	default:
		respondJSONRPCError(w, a2a.JSONRPCMethodNotFound, "Unknown method: "+req.Method, nil, req.ID)
	}
}

// handleTaskSubmit processes SendMessage requests.
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

// handleTaskGet processes GetTask requests.
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

// handleTaskCancel processes CancelTask requests.
func (a *API) handleTaskCancel(w http.ResponseWriter, r *http.Request, req *a2a.JSONRPCRequest) {
	ctx := r.Context()

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

	task, err := a.Services.TaskManager.CancelTask(ctx, params.TaskID)
	if err != nil {
		slog.Error("a2a: task cancel failed", "error", err, "taskId", params.TaskID)
		code, msg := mapTaskErrorToJSONRPC(err)
		respondJSONRPCError(w, code, msg, err.Error(), req.ID)
		return
	}

	respondJSONRPCSuccess(w, a2a.TaskCancelResponse{
		TaskID: task.ID,
		State:  task.State,
	}, req.ID)
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
	// service.ErrWorkflowCancelUnsupported is a sentinel — check with
	// errors.Is before falling back to substring matching below.
	if errors.Is(err, service.ErrWorkflowCancelUnsupported) {
		return a2a.ErrTaskNotCancelable, "Task cancellation not supported for this target kind"
	}

	// Check for specific error types from service layer
	errMsg := err.Error()

	// Map common errors
	if strings.Contains(errMsg, "already in terminal state") {
		return a2a.ErrTaskNotCancelable, "Task cannot be canceled"
	}
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
