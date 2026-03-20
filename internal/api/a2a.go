package api

import (
	"net/http"

	"github.com/hollis-labs/conduit/internal/store"
)

// handleA2AInbox returns messages for an agent's inbox.
func (a *API) handleA2AInbox(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	if agentID == "" {
		a.errorResp(w, http.StatusBadRequest, "agent_id is required")
		return
	}
	status := r.URL.Query().Get("status")

	msgs, err := a.Store.GetA2AInbox(agentID, status)
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

	msgs, err := a.Store.GetA2AThread(threadID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, msgs)
}

// handleA2ASendMessage sends a new A2A message.
func (a *API) handleA2ASendMessage(w http.ResponseWriter, r *http.Request) {
	var msg store.A2AMessage
	if err := a.decode(r, &msg); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if msg.FromAgent == "" || msg.ToAgent == "" || msg.Body == "" {
		a.errorResp(w, http.StatusBadRequest, "from_agent, to_agent, and body are required")
		return
	}

	if err := a.Store.SendA2AMessage(&msg); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, msg)
}

// handleA2AAck marks a message as read.
func (a *API) handleA2AAck(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.errorResp(w, http.StatusBadRequest, "id is required")
		return
	}
	if err := a.Store.AckA2AMessage(id); err != nil {
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
	if err := a.Store.ResolveA2AMessage(id); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "resolved"})
}

// handleA2AUnreadCount returns the unread message count for an agent.
func (a *API) handleA2AUnreadCount(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	if agentID == "" {
		a.errorResp(w, http.StatusBadRequest, "agent_id is required")
		return
	}
	count, err := a.Store.A2AUnreadCount(agentID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]int{"count": count})
}
