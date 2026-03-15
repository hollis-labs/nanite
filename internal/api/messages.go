package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/hollis-labs/conduit/internal/chat"
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

	msgID, err := a.Engine.HandleMessage(req.SessionID, req.Content)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
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

	msgID, err := a.Engine.SendAgentMessage(req.FromSessionID, toSessionID, req.Content)
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

	result, err := a.Engine.DelegateTask(r.Context(), chat.DelegationRequest{
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

	result, err := a.Engine.DelegateAndAggregate(r.Context(), parentSessionID, req.Message, model)
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

	msgID, err := a.Engine.RetryLastMessage(sessionID)
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

	ch, ok := a.Engine.GetStream(messageID)
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

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
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
