package api

import (
	"net/http"

	"github.com/hollis-labs/nanite/internal/store"
)

// --- Reminders (D1 / D2, CW-20260428-0014/0015) ---
//
// Surface session + project-scoped reminders for the FE Work panel and
// expose scope updates so the UI can promote/demote a reminder between
// session and project scope.

// handleListReminders — GET /api/sessions/{id}/reminders
// Returns all unfired reminders visible to the session, including
// project-scoped reminders attached to the session's project.
func (a *API) handleListReminders(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	rems, err := a.Services.Store.ListUnfiredReminders(sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to list reminders: "+err.Error())
		return
	}
	if rems == nil {
		rems = []store.Reminder{}
	}
	a.jsonResp(w, http.StatusOK, rems)
}

// handleDeleteReminder — DELETE /api/reminders/{id}
func (a *API) handleDeleteReminder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Services.Store.DeleteReminder(id); err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to delete reminder: "+err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
}

// reminderScopeReq is the body shape for PATCH /api/reminders/{id}/scope.
type reminderScopeReq struct {
	Scope     string `json:"scope"`
	ProjectID string `json:"project_id"`
}

// handleUpdateReminderScope — PATCH /api/reminders/{id}/scope
// Promotes/demotes a reminder between scopes. When the FE moves a reminder
// from session to project scope it must supply project_id (sourced from the
// originating session's project_id). Returns the updated reminder.
func (a *API) handleUpdateReminderScope(w http.ResponseWriter, r *http.Request) {
	var req reminderScopeReq
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	id := r.PathValue("id")
	if err := a.Services.Store.UpdateReminderScope(id, req.Scope, req.ProjectID); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	rem, err := a.Services.Store.GetReminder(id)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, rem)
}
