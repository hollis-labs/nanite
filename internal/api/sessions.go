package api

import (
	"net/http"
	"strconv"

	"github.com/hollis-labs/nanite/internal/store"
)

func (a *API) handleListSessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	workspaceID := q.Get("workspace_id")
	if workspaceID == "" {
		a.errorResp(w, http.StatusBadRequest, "workspace_id query parameter is required")
		return
	}

	includeArchived := q.Get("include_archived") == "true"

	sessions, err := a.Services.Store.ListSessions(workspaceID, includeArchived)
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
		AgentID     string `json:"agent_id"`
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
	if err := a.Services.Store.CreateSession(sess); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Resolve agent: request param → user settings default → mentat-001.
	agentID := req.AgentID
	if agentID == "" {
		if settings, err := a.Services.Store.GetUserSettings(); err == nil && settings.DefaultAgent != "" {
			agentID = settings.DefaultAgent
		}
	}
	if agentID == "" {
		agentID = "mentat-001"
	}

	// Assign the resolved agent as primary.
	if err := a.Services.Store.EnsureSessionAgent(sess.ID, agentID, "default", true); err != nil {
		// Log but don't fail — session was created successfully.
		_ = err
	}

	// Emit session creation event to Volon (fire-and-forget).
	if a.Services.Activity != nil {
		go a.Services.Activity.EmitSessionCreated(r.Context(), sess.ID, sess.WorkspaceID)
	}

	a.jsonResp(w, http.StatusCreated, sess)
}

func (a *API) handleForkSession(w http.ResponseWriter, r *http.Request) {
	sourceID := r.PathValue("id")

	var req struct {
		IncludeMessages bool   `json:"include_messages"`
		Provider        string `json:"provider"`
		Model           string `json:"model"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	overrides := &store.Session{
		Provider: req.Provider,
		Model:    req.Model,
	}

	newSess, err := a.Services.Store.ForkSession(sourceID, overrides, req.IncludeMessages)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResp(w, http.StatusCreated, newSess)
}

func (a *API) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, err := a.Services.Store.GetSession(id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "session not found")
		return
	}

	// Also return recent messages.
	messages, err := a.Services.Store.ListMessages(id, 50)
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

	existing, err := a.Services.Store.GetSession(id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "session not found")
		return
	}

	var req struct {
		Title      *string `json:"title"`
		CustomName *string `json:"custom_name"`
		IsPinned   *bool   `json:"is_pinned"`
		Model      *string `json:"model"`
		Provider   *string `json:"provider"`
		Status     *string `json:"status"`
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
	if req.Provider != nil {
		existing.Provider = *req.Provider
	}
	if req.Status != nil {
		switch *req.Status {
		case "active", "paused", "archived":
			existing.Status = *req.Status
		default:
			a.errorResp(w, http.StatusBadRequest, "status must be active, paused, or archived")
			return
		}
	}

	if err := a.Services.Store.UpdateSession(existing); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Emit plugin event when session is archived via update.
	if req.Status != nil && *req.Status == "archived" && a.Services.Plugins != nil {
		go a.Services.Plugins.EmitSessionArchived(id)
	}

	a.jsonResp(w, http.StatusOK, existing)
}

func (a *API) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Services.Store.ArchiveSession(id); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Kill any orphaned CLI processes for this session.
	if a.Services.ProcessTracker != nil {
		a.Services.ProcessTracker.KillSession(id)
	}

	// Broadcast session archived presence so UI updates immediately.
	a.Services.Streams.BroadcastSessionArchived(id)

	// Emit session ended event to Volon (fire-and-forget).
	if a.Services.Activity != nil {
		go a.Services.Activity.EmitSessionEnded(r.Context(), id)
	}

	// Emit plugin event: session archived.
	if a.Services.Plugins != nil {
		go a.Services.Plugins.EmitSessionArchived(id)
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
	sa, err := a.Services.Store.GetSessionPrimaryAgent(sessionID)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "no primary agent for session")
		return
	}

	// Verify the mode exists for this agent.
	if _, err := a.Services.Store.GetAgentMode(sa.AgentID, req.Mode); err != nil {
		a.errorResp(w, http.StatusBadRequest, "unknown mode: "+req.Mode)
		return
	}

	previousMode := sa.Mode

	// Update the mode.
	if err := a.Services.Store.SetSessionAgentMode(sessionID, sa.AgentID, req.Mode); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Emit plugin event: mode changed.
	if a.Services.Plugins != nil {
		go a.Services.Plugins.EmitModeChanged(sessionID, previousMode, req.Mode)
	}

	// Return updated session info.
	sess, err := a.Services.Store.GetSession(sessionID)
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
	messages, err := a.Services.Store.ListMessages(sessionID, 1000)
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
	if err := a.Services.Store.UpdateSessionCompaction(sessionID, summary); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Mark all messages as compacted.
	for _, m := range messages {
		if !m.IsCompacted {
			_ = a.Services.Store.UpdateMessageContent(m.ID, m.Content, true)
		}
	}

	a.jsonResp(w, http.StatusOK, map[string]string{"summary": summary})
}

func (a *API) handleListSessionMessages(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	q := r.URL.Query()

	limit := 50
	if l := q.Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	// "around" param: return a window centered on a specific message ID.
	if around := q.Get("around"); around != "" {
		before := 25
		after := 25
		if b := q.Get("before"); b != "" {
			if n, err := strconv.Atoi(b); err == nil && n >= 0 {
				before = n
			}
		}
		if af := q.Get("after"); af != "" {
			if n, err := strconv.Atoi(af); err == nil && n >= 0 {
				after = n
			}
		}
		page, err := a.Services.Store.ListMessagesAroundID(sessionID, around, before, after)
		if err != nil {
			a.errorResp(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.jsonResp(w, http.StatusOK, page)
		return
	}

	// Offset-based pagination.
	offset := 0
	if o := q.Get("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 {
			offset = n
		}
	}

	page, err := a.Services.Store.ListMessagesPaginated(sessionID, limit, offset)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, page)
}
