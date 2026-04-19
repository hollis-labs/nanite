package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/hollis-labs/nanite/internal/chat"
)

func (a *API) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	var req SendMessageRequest
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

	a.jsonResp(w, http.StatusAccepted, map[string]string{
		"message_id": msgID,
		"stream_url": fmt.Sprintf("/api/stream/%s", msgID),
	})
}

func (a *API) handleAgentMessage(w http.ResponseWriter, r *http.Request) {
	toSessionID := r.PathValue("id")

	var req AgentMessageRequest
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

	var req DelegateTaskRequest
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

	var req DelegateAndAggregateRequest
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
		model = a.Services.UtilityModel
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

	// CW-20260418-0100 / CW-20260419-0014: resume cursor has two sources:
	//   1. ?from=<uint64> query param — explicit, app-controlled.
	//   2. Last-Event-ID header — sent automatically by browser EventSource
	//      on auto-reconnect after a network blip, per the SSE spec.
	// Query param takes precedence when both are present (explicit override).
	// Missing/zero from either means "give me everything the ring buffer
	// still holds". Reject malformed ?from= with 400 so frontend bugs
	// surface instead of being silently coerced to zero.
	//
	// Why this matters (the c13/c14 UAT bug): before this, browser
	// EventSource auto-reconnects on a network blip replayed from cursor=0,
	// which re-delivered every event in the ring buffer. The FE's
	// addToolCall is append-without-dedup, so tool-call counts doubled
	// (14 → 28, 16 → 32). With Last-Event-ID read here AND the SSE `id:`
	// line emitted below, the browser transparently resumes from where
	// it left off — no duplicate events.
	fromEventID := uint64(0)
	if raw := r.URL.Query().Get("from"); raw != "" {
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			a.errorResp(w, http.StatusBadRequest, fmt.Sprintf("invalid from value: %q", raw))
			return
		}
		fromEventID = n
	} else if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		if n, err := strconv.ParseUint(raw, 10, 64); err == nil {
			fromEventID = n
		}
		// Malformed Last-Event-ID is silently treated as zero — it comes
		// from the browser, not the app, so there's no caller bug to
		// surface with a 400.
	}

	// streamClosed just tells us the generation finished before we connected;
	// behavior is identical either way — the channel carries replay events
	// (if any) and then EOFs. The for/select below handles both paths.
	ch, _, ok := a.Services.Streams.Subscribe(messageID, fromEventID)
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
	//
	// Note: when streamClosed=true the generation has already finished; we
	// still drain the replay channel so the client can pick up missed final
	// events before falling back to /api/sessions/{id}/messages.
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
			// Emit an SSE `id:` line so browser EventSource auto-reconnect
			// can send Last-Event-ID and resume via the ring buffer.
			// CW-20260419-0014. Zero-EventID events (synthetic, pre-pump)
			// are emitted without an id: line so they don't clobber the
			// browser's stored id.
			if evt.EventID > 0 {
				fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", evt.EventID, evt.Type, data)
			} else {
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Type, data)
			}
			flusher.Flush()
		}
	}
}
