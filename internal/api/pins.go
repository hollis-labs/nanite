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
	pins, err := a.Services.Store.ListPinnedContent(sessionID)
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
	if err := a.Services.Store.DeletePinnedContent(id); err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to delete pin: "+err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
}
