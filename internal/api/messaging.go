package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/hollis-labs/nanite/internal/messaging"
)

// messagingStatus returns the HTTP status code for a messaging.Service
// error: 400 for validation, 403 for forbidden, 404 for not-found, 500
// otherwise.
func messagingStatus(err error) int {
	if errors.Is(err, messaging.ErrValidation) {
		return http.StatusBadRequest
	}
	if errors.Is(err, messaging.ErrForbidden) {
		return http.StatusForbidden
	}
	if errors.Is(err, messaging.ErrNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

// handleMessageInbox returns messages for a (session_id, agent_id)
// inbox. MVP caller identity: the HTTP boundary has no session-cookie-
// based auth yet, so the query's session_id/agent_id serve as both
// target and caller. The service still enforces the match, which
// catches misconfigured callers passing different values.
func (a *API) handleMessageInbox(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	agentID := r.URL.Query().Get("agent_id")
	if sessionID == "" || agentID == "" {
		a.errorResp(w, http.StatusBadRequest, "session_id and agent_id are required")
		return
	}
	filter := messaging.InboxFilter{
		Status:  r.URL.Query().Get("status"),
		Channel: r.URL.Query().Get("channel"),
		Kind:    r.URL.Query().Get("kind"),
	}

	msgs, err := a.Services.Messaging.Inbox(r.Context(), sessionID, agentID, filter, sessionID, agentID)
	if err != nil {
		a.errorResp(w, messagingStatus(err), err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, msgs)
}

// handleMessageThread returns all messages in a thread that the caller
// is a participant of (from_* or to_*). Non-participants see an empty
// slice. Caller identity is taken from the query's session_id+agent_id
// parameters (same MVP shape as Inbox).
func (a *API) handleMessageThread(w http.ResponseWriter, r *http.Request) {
	threadID := r.PathValue("threadId")
	if threadID == "" {
		a.errorResp(w, http.StatusBadRequest, "threadId is required")
		return
	}
	callerSessionID := r.URL.Query().Get("session_id")
	callerAgentID := r.URL.Query().Get("agent_id")
	if callerSessionID == "" || callerAgentID == "" {
		a.errorResp(w, http.StatusBadRequest, "session_id and agent_id are required")
		return
	}

	msgs, err := a.Services.Messaging.Thread(r.Context(), threadID, callerSessionID, callerAgentID)
	if err != nil {
		a.errorResp(w, messagingStatus(err), err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, msgs)
}

// handleMessageSend sends a new message. The caller must supply
// from_session_id, from_agent_id, to_session_id, to_agent_id, and body.
// Validation errors become 400; DB and other internal errors become
// 500.
func (a *API) handleMessageSend(w http.ResponseWriter, r *http.Request) {
	var in messaging.SendInput
	if err := a.decode(r, &in); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	saved, err := a.Services.Messaging.SendMessage(r.Context(), in)
	if err != nil {
		a.errorResp(w, messagingStatus(err), err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, saved)
}

// handleMessageAck marks a message as read. The caller's (session_id,
// agent_id) comes from the JSON body and must match the persisted
// recipient (enforced by the service).
func (a *API) handleMessageAck(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.errorResp(w, http.StatusBadRequest, "id is required")
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
		AgentID   string `json:"agent_id"`
	}
	// Treat an empty body as zero-value req so the next validation
	// block produces a descriptive "session_id and agent_id are
	// required" 400 instead of the opaque "invalid JSON body".
	if err := a.decode(r, &req); err != nil && !errors.Is(err, io.EOF) {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.SessionID == "" || req.AgentID == "" {
		a.errorResp(w, http.StatusBadRequest, "session_id and agent_id are required")
		return
	}

	if err := a.Services.Messaging.Ack(r.Context(), req.SessionID, req.AgentID, id); err != nil {
		a.errorResp(w, messagingStatus(err), err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "read"})
}

// handleMessageResolve marks a message as resolved. Same recipient
// check as Ack.
func (a *API) handleMessageResolve(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.errorResp(w, http.StatusBadRequest, "id is required")
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
		AgentID   string `json:"agent_id"`
	}
	if err := a.decode(r, &req); err != nil && !errors.Is(err, io.EOF) {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.SessionID == "" || req.AgentID == "" {
		a.errorResp(w, http.StatusBadRequest, "session_id and agent_id are required")
		return
	}

	if err := a.Services.Messaging.Resolve(r.Context(), req.SessionID, req.AgentID, id); err != nil {
		a.errorResp(w, messagingStatus(err), err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "resolved"})
}

// handleMessageUnreadCount returns the unread message count for a
// (session_id, agent_id) inbox.
func (a *API) handleMessageUnreadCount(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	agentID := r.URL.Query().Get("agent_id")
	if sessionID == "" || agentID == "" {
		a.errorResp(w, http.StatusBadRequest, "session_id and agent_id are required")
		return
	}

	count, err := a.Services.Messaging.UnreadCount(r.Context(), sessionID, agentID)
	if err != nil {
		a.errorResp(w, messagingStatus(err), err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]int{"count": count})
}

// handleHandoffRequest creates a pending handoff from one agent to
// another.
func (a *API) handleHandoffRequest(w http.ResponseWriter, r *http.Request) {
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
	id, err := a.Services.Messaging.RequestHandoff(r.Context(), req.SessionID, req.FromAgentID, req.ToAgentID, req.RequestedBy)
	if err != nil {
		a.errorResp(w, messagingStatus(err), err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, map[string]string{"id": id})
}

// handleHandoffApprove atomically completes a pending handoff.
func (a *API) handleHandoffApprove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.errorResp(w, http.StatusBadRequest, "id is required")
		return
	}
	if err := a.Services.Messaging.ApproveHandoff(r.Context(), id); err != nil {
		a.errorResp(w, messagingStatus(err), err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "completed"})
}

// handleHandoffReject marks a pending handoff as rejected.
func (a *API) handleHandoffReject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.errorResp(w, http.StatusBadRequest, "id is required")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	// Empty body is fine (reason is optional); malformed JSON still
	// rejected rather than silently becoming "no reason".
	if err := a.decode(r, &req); err != nil && !errors.Is(err, io.EOF) {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := a.Services.Messaging.RejectHandoff(r.Context(), id, req.Reason); err != nil {
		a.errorResp(w, messagingStatus(err), err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "rejected"})
}

// handleMessageRecent returns the most recent messages in a session,
// capped by limit.
func (a *API) handleMessageRecent(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		a.errorResp(w, http.StatusBadRequest, "session_id is required")
		return
	}
	limit := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil {
			a.errorResp(w, http.StatusBadRequest, "limit must be an integer")
			return
		}
		limit = n
	}
	msgs, err := a.Services.Messaging.RecentForSession(r.Context(), sessionID, limit)
	if err != nil {
		a.errorResp(w, messagingStatus(err), err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, msgs)
}
