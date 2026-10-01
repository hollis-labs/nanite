package api

import (
	"net/http"

	permissionlib "github.com/hollis-labs/go-permission"
)

// handleRespondApproval handles POST /api/sessions/{id}/approvals/{requestId}.
// The client calls this to allow or deny a pending tool permission request.
func (a *API) handleRespondApproval(w http.ResponseWriter, r *http.Request) {
	if a.Services.Permissions == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "permission engine not configured")
		return
	}

	sessionID := r.PathValue("id")
	requestID := r.PathValue("requestId")
	if requestID == "" {
		a.errorResp(w, http.StatusBadRequest, "missing request ID")
		return
	}

	var req RespondApprovalRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid request body")
		return
	}

	decision := permissionlib.Decision(req.Decision)
	if decision != permissionlib.DecisionAllow && decision != permissionlib.DecisionDeny {
		a.errorResp(w, http.StatusBadRequest, "decision must be 'allow' or 'deny'")
		return
	}

	scope := permissionlib.Scope(req.Scope)
	switch scope {
	case permissionlib.ScopeOnce, permissionlib.ScopeSession:
		// supported
	case "":
		scope = permissionlib.ScopeOnce
	default:
		a.errorResp(w, http.StatusBadRequest, "scope must be 'once' or 'session'")
		return
	}

	ok := a.Services.Permissions.Respond(requestID, decision, scope, sessionID)
	if !ok {
		a.errorResp(w, http.StatusNotFound, "approval request not found or already resolved")
		return
	}

	a.jsonResp(w, http.StatusOK, map[string]string{"status": "accepted"})
}

// handleGetPermissionMode handles GET /api/permissions/mode.
func (a *API) handleGetPermissionMode(w http.ResponseWriter, r *http.Request) {
	if a.Services.Permissions == nil {
		a.jsonResp(w, http.StatusOK, map[string]string{"mode": "default"})
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"mode": string(a.Services.Permissions.Mode())})
}

// handleSetPermissionMode handles PUT /api/permissions/mode.
func (a *API) handleSetPermissionMode(w http.ResponseWriter, r *http.Request) {
	if a.Services.Permissions == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "permission engine not configured")
		return
	}

	var req SetPermissionModeRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid request body")
		return
	}

	mode := permissionlib.Mode(req.Mode)
	switch mode {
	case permissionlib.ModeDefault, permissionlib.ModeAcceptEdits, permissionlib.ModePlan, permissionlib.ModeYolo:
		// valid
	default:
		a.errorResp(w, http.StatusBadRequest, "invalid mode — must be default, accept-edits, plan, or yolo")
		return
	}

	a.Services.Permissions.SetMode(mode)
	a.jsonResp(w, http.StatusOK, map[string]string{"mode": string(mode)})
}
