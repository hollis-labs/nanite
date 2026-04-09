package api

import (
	"net/http"

	"github.com/hollis-labs/nanite/internal/store"
)

// handleA2AInbox returns messages for a (session_id, agent_id) inbox.
// NOTE: this handler is permissive pending Task 8, which will route all A2A
// traffic through the service layer for agent/session validation.
func (a *API) handleA2AInbox(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	agentID := r.URL.Query().Get("agent_id")
	if sessionID == "" || agentID == "" {
		a.errorResp(w, http.StatusBadRequest, "session_id and agent_id are required")
		return
	}
	status := r.URL.Query().Get("status")

	msgs, err := a.Services.Store.GetA2AInbox(sessionID, agentID, status)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, msgs)
}

// handleA2AThread returns all messages in a thread.
func (a *API) handleA2AThread(w http.ResponseWriter, r *http.Request) {
	threadID := r.PathValue("threadId")
	if threadID == "" {
		a.errorResp(w, http.StatusBadRequest, "threadId is required")
		return
	}

	msgs, err := a.Services.Store.GetA2AThread(threadID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, msgs)
}

// handleA2ASendMessage sends a new A2A message. The caller must supply
// from_session_id, from_agent_id, to_session_id, to_agent_id, and body.
func (a *API) handleA2ASendMessage(w http.ResponseWriter, r *http.Request) {
	var msg store.A2AMessage
	if err := a.decode(r, &msg); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if msg.FromSessionID == "" || msg.FromAgentID == "" ||
		msg.ToSessionID == "" || msg.ToAgentID == "" || msg.Body == "" {
		a.errorResp(w, http.StatusBadRequest,
			"from_session_id, from_agent_id, to_session_id, to_agent_id, and body are required")
		return
	}

	saved, err := a.Services.Store.SendA2AMessage(&msg)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, saved)
}

// handleA2AAck marks a message as read.
func (a *API) handleA2AAck(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.errorResp(w, http.StatusBadRequest, "id is required")
		return
	}

	if err := a.Services.Store.AckA2AMessage(id); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "read"})
}

// handleA2AResolve marks a message as resolved.
func (a *API) handleA2AResolve(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.errorResp(w, http.StatusBadRequest, "id is required")
		return
	}

	if err := a.Services.Store.ResolveA2AMessage(id); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "resolved"})
}

// handleA2AUnreadCount returns the unread message count for a
// (session_id, agent_id) inbox.
func (a *API) handleA2AUnreadCount(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	agentID := r.URL.Query().Get("agent_id")
	if sessionID == "" || agentID == "" {
		a.errorResp(w, http.StatusBadRequest, "session_id and agent_id are required")
		return
	}

	count, err := a.Services.Store.A2AUnreadCount(sessionID, agentID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]int{"count": count})
}
