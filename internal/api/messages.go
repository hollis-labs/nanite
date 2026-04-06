package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/hollis-labs/nanite/internal/chat"
)

func (a *API) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"session_id"`
		Content   string `json:"content"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.SessionID == "" || req.Content == "" {
		a.errorResp(w, http.StatusBadRequest, "session_id and content are required")
		return
	}

	msgID, err := a.Services.Chat.HandleMessage(r.Context(), req.SessionID, req.Content)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Emit message.sent plugin event (fire-and-forget). Note: HandleMessage
	// creates its own user-message ID internally; msgID here is the assistant
	// message ID for the response stream. Plugins wanting the precise user
	// message ID can look it up via session history.
	if a.Services.Plugins != nil {
		go a.Services.Plugins.EmitMessageSent(req.SessionID, "", req.Content, "user", 0)
	}

	a.jsonResp(w, http.StatusAccepted, map[string]string{
		"message_id": msgID,
		"stream_url": fmt.Sprintf("/api/stream/%s", msgID),
	})
}

func (a *API) handleAgentMessage(w http.ResponseWriter, r *http.Request) {
	toSessionID := r.PathValue("id")

	var req struct {
		FromSessionID string `json:"from_session_id"`
		Content       string `json:"content"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.FromSessionID == "" || req.Content == "" {
		a.errorResp(w, http.StatusBadRequest, "from_session_id and content are required")
		return
	}

	msgID, err := a.Services.Chat.SendAgentMessage(r.Context(), req.FromSessionID, toSessionID, req.Content)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResp(w, http.StatusAccepted, map[string]string{
		"message_id": msgID,
		"stream_url": fmt.Sprintf("/api/stream/%s", msgID),
	})
}

func (a *API) handleDelegateTask(w http.ResponseWriter, r *http.Request) {
	parentSessionID := r.PathValue("id")
	if parentSessionID == "" {
		a.errorResp(w, http.StatusBadRequest, "session id is required")
		return
	}

	var req struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		AgentID     string `json:"agent_id,omitempty"`
		Mode        string `json:"mode,omitempty"`
		Model       string `json:"model,omitempty"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Title == "" || req.Description == "" {
		a.errorResp(w, http.StatusBadRequest, "title and description are required")
		return
	}

	result, err := a.Services.Chat.DelegateTask(r.Context(), chat.DelegationRequest{
		ParentSessionID: parentSessionID,
		Title:           req.Title,
		Description:     req.Description,
		AgentID:         req.AgentID,
		Mode:            req.Mode,
		Model:           req.Model,
	})
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResp(w, http.StatusOK, result)
}

func (a *API) handleDelegateAndAggregate(w http.ResponseWriter, r *http.Request) {
	parentSessionID := r.PathValue("id")
	if parentSessionID == "" {
		a.errorResp(w, http.StatusBadRequest, "session id is required")
		return
	}

	var req struct {
		Message string `json:"message"`
		Model   string `json:"model,omitempty"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Message == "" {
		a.errorResp(w, http.StatusBadRequest, "message is required")
		return
	}

	model := req.Model
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	result, err := a.Services.Chat.DelegateAndAggregate(r.Context(), parentSessionID, req.Message, model)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResp(w, http.StatusOK, result)
}

func (a *API) handleRetryStream(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if sessionID == "" {
		a.errorResp(w, http.StatusBadRequest, "session_id is required")
		return
	}

	msgID, err := a.Services.Chat.RetryLastMessage(r.Context(), sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResp(w, http.StatusAccepted, map[string]string{
		"message_id": msgID,
		"stream_url": fmt.Sprintf("/api/stream/%s", msgID),
	})
}

func (a *API) handleStream(w http.ResponseWriter, r *http.Request) {
	messageID := r.PathValue("messageID")

	ch, ok := a.Services.Streams.GetStream(messageID)
	if !ok {
		a.errorResp(w, http.StatusNotFound, "stream not found")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		a.errorResp(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Register this SSE connection for session-level deduplication.
	// If another tab already has an active SSE connection for this session,
	// it will receive a session_takeover event and be closed.
	var sseDone <-chan struct{}
	if sessionID, found := a.Services.Streams.GetSessionForMessage(messageID); found {
		sseDone = a.Services.Streams.RegisterSSE(sessionID)
		defer a.Services.Streams.UnregisterSSE(sessionID, sseDone)
	}

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sseDone:
			// Another tab opened an SSE connection for this session — send takeover event and close.
			evt := chat.StreamEvent{Type: "session_takeover", Content: "This session is now active in another tab"}
			data, _ := json.Marshal(evt)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Type, data)
			flusher.Flush()
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(evt)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Type, data)
			flusher.Flush()
		}
	}
}
