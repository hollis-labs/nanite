package api

import (
	"net/http"
	"strconv"

	"github.com/hollis-labs/mentat-chat/internal/store"
)

func (a *API) handleListSessions(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.URL.Query().Get("workspace_id")
	if workspaceID == "" {
		a.errorResp(w, http.StatusBadRequest, "workspace_id query parameter is required")
		return
	}

	sessions, err := a.Store.ListSessions(workspaceID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, sessions)
}

func (a *API) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkspaceID string `json:"workspace_id"`
		ProjectID   string `json:"project_id"`
		Model       string `json:"model"`
		Provider    string `json:"provider"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.WorkspaceID == "" {
		a.errorResp(w, http.StatusBadRequest, "workspace_id is required")
		return
	}

	sess := &store.Session{
		WorkspaceID: req.WorkspaceID,
		ProjectID:   req.ProjectID,
		Model:       req.Model,
		Provider:    req.Provider,
	}
	if err := a.Store.CreateSession(sess); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, sess)
}

func (a *API) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, err := a.Store.GetSession(id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "session not found")
		return
	}

	// Also return recent messages.
	messages, err := a.Store.ListMessages(id, 50)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResp(w, http.StatusOK, map[string]any{
		"session":  sess,
		"messages": messages,
	})
}

func (a *API) handleUpdateSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	existing, err := a.Store.GetSession(id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "session not found")
		return
	}

	var req struct {
		Title      *string `json:"title"`
		CustomName *string `json:"custom_name"`
		IsPinned   *bool   `json:"is_pinned"`
		Model      *string `json:"model"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.Title != nil {
		existing.Title = *req.Title
	}
	if req.CustomName != nil {
		existing.CustomName = *req.CustomName
	}
	if req.IsPinned != nil {
		existing.IsPinned = *req.IsPinned
	}
	if req.Model != nil {
		existing.Model = *req.Model
	}

	if err := a.Store.UpdateSession(existing); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, existing)
}

func (a *API) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Store.ArchiveSession(id); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"archived": id})
}

func (a *API) handleListSessionMessages(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	messages, err := a.Store.ListMessages(sessionID, limit)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, messages)
}
