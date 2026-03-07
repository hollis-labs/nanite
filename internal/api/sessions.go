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

	// Auto-assign the Mentat agent as primary.
	if err := a.Store.EnsureSessionAgent(sess.ID, "mentat-001", "default", true); err != nil {
		// Log but don't fail — session was created successfully.
		_ = err
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

func (a *API) handleSwitchSessionMode(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	var req struct {
		Mode string `json:"mode"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Mode == "" {
		a.errorResp(w, http.StatusBadRequest, "mode is required")
		return
	}

	// Get the primary agent for this session.
	sa, err := a.Store.GetSessionPrimaryAgent(sessionID)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "no primary agent for session")
		return
	}

	// Verify the mode exists for this agent.
	if _, err := a.Store.GetAgentMode(sa.AgentID, req.Mode); err != nil {
		a.errorResp(w, http.StatusBadRequest, "unknown mode: "+req.Mode)
		return
	}

	// Update the mode.
	if err := a.Store.SetSessionAgentMode(sessionID, sa.AgentID, req.Mode); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Return updated session info.
	sess, err := a.Store.GetSession(sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResp(w, http.StatusOK, map[string]any{
		"session": sess,
		"mode":    req.Mode,
	})
}

func (a *API) handleCompactSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	// Load all messages for the session.
	messages, err := a.Store.ListMessages(sessionID, 1000)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// MVP: concatenate all message contents, truncate to 2000 chars.
	var total int
	var summary string
	for _, m := range messages {
		if total+len(m.Content) > 2000 {
			summary += m.Content[:2000-total]
			total = 2000
			break
		}
		summary += m.Content + "\n"
		total += len(m.Content) + 1
	}

	// Save compaction summary on session.
	if err := a.Store.UpdateSessionCompaction(sessionID, summary); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Mark all messages as compacted.
	for _, m := range messages {
		if !m.IsCompacted {
			_ = a.Store.UpdateMessageContent(m.ID, m.Content, true)
		}
	}

	a.jsonResp(w, http.StatusOK, map[string]string{"summary": summary})
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
