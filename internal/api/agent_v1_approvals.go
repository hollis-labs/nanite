package api

import (
	"errors"
	"net/http"

	permissionlib "github.com/hollis-labs/go-permission"
	"github.com/hollis-labs/nanite/internal/service"
)

func (a *API) handleAgentV1ApprovalResponse(w http.ResponseWriter, r *http.Request) {
	view := r.PathValue("id")
	if _, err := a.Services.Sessions.Get(r.Context(), view); err != nil {
		a.agentV1LookupError(w, err)
		return
	}
	if a.Services.CognitiveApprovals == nil {
		a.agentV1Error(w, http.StatusServiceUnavailable, "native approval responses unavailable")
		return
	}
	var req RespondApprovalRequest
	if err := a.decodeAgentV1(r, &req); err != nil {
		a.agentV1Error(w, http.StatusBadRequest, "invalid approval response")
		return
	}
	decision := permissionlib.Decision(req.Decision)
	if decision != permissionlib.DecisionAllow && decision != permissionlib.DecisionDeny {
		a.agentV1Error(w, http.StatusBadRequest, "decision must be allow or deny")
		return
	}
	if req.Scope == "" {
		req.Scope = string(permissionlib.ScopeOnce)
	}
	if req.Scope != string(permissionlib.ScopeOnce) {
		a.agentV1Error(w, http.StatusUnprocessableEntity, "only once scope is supported")
		return
	}
	response, err := a.Services.CognitiveApprovals.Respond(r.Context(), view, r.PathValue("requestId"), decision, permissionlib.ScopeOnce)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrCognitiveApprovalNotFound):
			a.agentV1Error(w, http.StatusNotFound, "approval not found in this view")
		case errors.Is(err, service.ErrCognitiveApprovalConflict):
			a.agentV1Error(w, http.StatusConflict, err.Error())
		default:
			a.agentV1Error(w, http.StatusInternalServerError, "approval response failed")
		}
		return
	}
	a.jsonResp(w, http.StatusOK, response)
}
