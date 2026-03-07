package api

import (
	"encoding/json"
	"fmt"
	"net/http"
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
