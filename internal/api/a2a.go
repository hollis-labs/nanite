package api

import (
	"net/http"
	"strconv"

	"github.com/hollis-labs/nanite/internal/store"
)

// handleA2AInbox returns messages for a (session_id, agent_id) inbox.
// Routes through a2a.Service so validation stays in one place.
func (a *API) handleA2AInbox(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	agentID := r.URL.Query().Get("agent_id")
	if sessionID == "" || agentID == "" {
		a.errorResp(w, http.StatusBadRequest, "session_id and agent_id are required")
		return
	}
	status := r.URL.Query().Get("status")

	msgs, err := a.Services.A2A.Inbox(r.Context(), sessionID, agentID, status)
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

	msgs, err := a.Services.A2A.Thread(r.Context(), threadID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, msgs)
}

// handleA2ASendMessage sends a new A2A message. The caller must supply
// from_session_id, from_agent_id, to_session_id, to_agent_id, and body.
// Validation happens inside the service, so errors here are surfaced as 400.
func (a *API) handleA2ASendMessage(w http.ResponseWriter, r *http.Request) {
	var msg store.A2AMessage
	if err := a.decode(r, &msg); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	saved, err := a.Services.A2A.SendMessage(r.Context(), &msg)
	if err != nil {
		// The service only errors on validation or DB issues. Returning 400
		// for both is acceptable for MVP — validation is by far the common case.
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, saved)
}

// handleA2AAck marks a message as read. The caller's (session_id, agent_id)
// comes from the JSON body — accepted for future impersonation checks.
func (a *API) handleA2AAck(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.errorResp(w, http.StatusBadRequest, "id is required")
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
		AgentID   string `json:"agent_id"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.SessionID == "" || req.AgentID == "" {
		a.errorResp(w, http.StatusBadRequest, "session_id and agent_id are required")
		return
	}

	if err := a.Services.A2A.Ack(r.Context(), req.SessionID, req.AgentID, id); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "read"})
}

// handleA2AResolve marks a message as resolved. Same caller-identification
// requirement as Ack.
func (a *API) handleA2AResolve(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.errorResp(w, http.StatusBadRequest, "id is required")
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
		AgentID   string `json:"agent_id"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.SessionID == "" || req.AgentID == "" {
		a.errorResp(w, http.StatusBadRequest, "session_id and agent_id are required")
		return
	}

	if err := a.Services.A2A.Resolve(r.Context(), req.SessionID, req.AgentID, id); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "resolved"})
}

// handleA2AUnreadCount returns the unread message count for a
// (session_id, agent_id) inbox. There is no wrapper on a2a.Service for this,
// so it remains a direct store call.
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

// handleA2AHandoffRequest creates a pending handoff from one agent to another.
func (a *API) handleA2AHandoffRequest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID   string `json:"session_id"`
		FromAgentID string `json:"from_agent_id"` // optional
		ToAgentID   string `json:"to_agent_id"`
		RequestedBy string `json:"requested_by"` // "departing" | "incoming" | "user"
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	id, err := a.Services.A2A.RequestHandoff(r.Context(), req.SessionID, req.FromAgentID, req.ToAgentID, req.RequestedBy)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, map[string]string{"id": id})
}

// handleA2AHandoffApprove atomically completes a pending handoff.
func (a *API) handleA2AHandoffApprove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.errorResp(w, http.StatusBadRequest, "id is required")
		return
	}
	if err := a.Services.A2A.ApproveHandoff(r.Context(), id); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "completed"})
}

// handleA2AHandoffReject marks a pending handoff as rejected.
func (a *API) handleA2AHandoffReject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.errorResp(w, http.StatusBadRequest, "id is required")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	_ = a.decode(r, &req) // reason is optional
	if err := a.Services.A2A.RejectHandoff(r.Context(), id, req.Reason); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "rejected"})
}

// handleA2ARecent returns the most recent messages in a session, capped by limit.
func (a *API) handleA2ARecent(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		a.errorResp(w, http.StatusBadRequest, "session_id is required")
		return
	}
	limit := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil {
			limit = n
		}
	}
	msgs, err := a.Services.A2A.RecentForSession(r.Context(), sessionID, limit)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, msgs)
}
