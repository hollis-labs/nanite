package api

import (
	"net/http"

	"github.com/hollis-labs/nanite/internal/store"
)

// --- Pinned content (J11, CW-20260426-0009) ---

// handleListPins — GET /api/sessions/{id}/pins
// Returns all session-scoped + cross_session pinned items for a session.
func (a *API) handleListPins(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	pins, err := a.Services.Store.ListPinnedContent(r.Context(), sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to list pins: "+err.Error())
		return
	}
	if pins == nil {
		pins = []store.PinnedContent{}
	}
	a.jsonResp(w, http.StatusOK, pins)
}

// handleDeletePin — DELETE /api/pins/{id}
// Removes a pinned item by ID. Called by the UI Pins tab unpin button.
func (a *API) handleDeletePin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Services.Store.DeletePinnedContent(r.Context(), id); err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to delete pin: "+err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
}

// pinScopeReq is the body shape for PATCH /api/pins/{id}/scope (D2).
type pinScopeReq struct {
	Scope     string `json:"scope"`
	ProjectID string `json:"project_id"`
}

// handleUpdatePinScope — PATCH /api/pins/{id}/scope
// Promotes/demotes a pin between session and project scope. The FE supplies
// project_id when moving a pin to project scope (sourced from the
// originating session's project_id).
func (a *API) handleUpdatePinScope(w http.ResponseWriter, r *http.Request) {
	var req pinScopeReq
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	id := r.PathValue("id")
	if err := a.Services.Store.UpdatePinScope(r.Context(), id, req.Scope, req.ProjectID); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]any{"updated": true, "id": id, "scope": req.Scope, "project_id": req.ProjectID})
}
