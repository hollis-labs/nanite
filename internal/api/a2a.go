package api

import (
	"net/http"
	"time"

	"github.com/hollis-labs/conduit/internal/store"
	"github.com/hollis-labs/nexus/messaging"
)

// nexusMessageToA2A converts a Nexus Message to the legacy A2AMessage JSON shape
// so the frontend contract stays identical.
func nexusMessageToA2A(m messaging.Message) store.A2AMessage {
	msg := store.A2AMessage{
		ID:        m.ID,
		FromAgent: m.FromAgent,
		ToAgent:   m.ToAgent,
		ThreadID:  m.ThreadID,
		ReplyTo:   m.ReplyTo,
		Type:      m.Type,
		Subject:   m.Subject,
		Body:      m.Body,
		Metadata:  m.Metadata,
		Priority:  m.Priority,
		Status:    m.Status,
		CreatedAt: m.CreatedAt.Format(time.RFC3339),
	}
	if m.ReadAt != nil {
		s := m.ReadAt.Format(time.RFC3339)
		msg.ReadAt = &s
	}
	if m.ResolvedAt != nil {
		s := m.ResolvedAt.Format(time.RFC3339)
		msg.ResolvedAt = &s
	}
	return msg
}

// nexusMessagesToA2A converts a slice of Nexus Messages.
func nexusMessagesToA2A(msgs []messaging.Message) []store.A2AMessage {
	out := make([]store.A2AMessage, len(msgs))
	for i, m := range msgs {
		out[i] = nexusMessageToA2A(m)
	}
	return out
}

// handleA2AInbox returns messages for an agent's inbox.
func (a *API) handleA2AInbox(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	if agentID == "" {
		a.errorResp(w, http.StatusBadRequest, "agent_id is required")
		return
	}
	status := r.URL.Query().Get("status")

	if a.NexusMsg != nil {
		var opts []messaging.InboxOption
		if status != "" {
			opts = append(opts, messaging.WithStatus(status))
		}
		msgs, err := a.NexusMsg.Inbox(r.Context(), agentID, opts...)
		if err != nil {
			a.errorResp(w, http.StatusInternalServerError, err.Error())
			return
		}
		if msgs == nil {
			msgs = []messaging.Message{}
		}
		a.jsonResp(w, http.StatusOK, nexusMessagesToA2A(msgs))
		return
	}

	// Fallback: SQLite store.
	msgs, err := a.Services.Store.GetA2AInbox(agentID, status)
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

	if a.NexusMsg != nil {
		msgs, err := a.NexusMsg.Thread(r.Context(), threadID)
		if err != nil {
			a.errorResp(w, http.StatusInternalServerError, err.Error())
			return
		}
		if msgs == nil {
			msgs = []messaging.Message{}
		}
		a.jsonResp(w, http.StatusOK, nexusMessagesToA2A(msgs))
		return
	}

	msgs, err := a.Services.Store.GetA2AThread(threadID)
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

	if a.NexusMsg != nil {
		input := messaging.SendInput{
			FromAgent: msg.FromAgent,
			ToAgent:   msg.ToAgent,
			Body:      msg.Body,
			Subject:   msg.Subject,
			Type:      msg.Type,
			ThreadID:  msg.ThreadID,
			ReplyTo:   msg.ReplyTo,
			Metadata:  msg.Metadata,
			Priority:  msg.Priority,
		}
		sent, err := a.NexusMsg.Send(r.Context(), input)
		if err != nil {
			a.errorResp(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.jsonResp(w, http.StatusCreated, nexusMessageToA2A(sent))
		return
	}

	if err := a.Services.Store.SendA2AMessage(&msg); err != nil {
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

	if a.NexusMsg != nil {
		if err := a.NexusMsg.Ack(r.Context(), id); err != nil {
			a.errorResp(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.jsonResp(w, http.StatusOK, map[string]string{"status": "read"})
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

	if a.NexusMsg != nil {
		if err := a.NexusMsg.Resolve(r.Context(), id); err != nil {
			a.errorResp(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.jsonResp(w, http.StatusOK, map[string]string{"status": "resolved"})
		return
	}

	if err := a.Services.Store.ResolveA2AMessage(id); err != nil {
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

	if a.NexusMsg != nil {
		count, err := a.NexusMsg.UnreadCount(r.Context(), agentID)
		if err != nil {
			a.errorResp(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.jsonResp(w, http.StatusOK, map[string]int{"count": count})
		return
	}

	count, err := a.Services.Store.A2AUnreadCount(agentID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]int{"count": count})
}
