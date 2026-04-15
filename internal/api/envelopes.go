package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

// defaultEnvelopeResponseHandlerTimeout caps the time a ResponseHandler can run
// before the endpoint returns 504. Keeps a slow handler (e.g. a ticket-API
// call) from stalling the HTTP request indefinitely.
const defaultEnvelopeResponseHandlerTimeout = 5 * time.Second

// handleEnvelopeRespond accepts a typed ResponseV1 payload from the frontend,
// validates it, dispatches to the registered ResponseHandler, persists both
// the response and an envelope_response transcript message, and returns the
// transcript message ID + optional follow-up. See plans/phase-3-s5-envelope-typed-responses.md §T4.
func (a *API) handleEnvelopeRespond(w http.ResponseWriter, r *http.Request) {
	envelopeID := r.PathValue("id")
	if envelopeID == "" {
		a.errorResp(w, http.StatusBadRequest, "envelope id required")
		return
	}

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	_ = r.Body.Close()
	resp, err := chat.UnmarshalResponseV1(raw)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	if resp.ID != "" && resp.ID != envelopeID {
		a.errorResp(w, http.StatusBadRequest, "response id does not match path id")
		return
	}
	resp.ID = envelopeID

	inst, err := a.Services.Store.GetEnvelopeInstance(envelopeID)
	if errors.Is(err, sql.ErrNoRows) {
		a.errorResp(w, http.StatusNotFound, "envelope not found")
		return
	}
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Idempotency: if the envelope already has a terminal response, return 409
	// with the prior stored response so duplicate submissions are safe.
	if inst.RespondedAt != nil {
		a.jsonResp(w, http.StatusConflict, map[string]any{
			"error":           "envelope already responded",
			"response_status": inst.ResponseStatus,
			"response":        json.RawMessage(inst.ResponseJSON),
		})
		return
	}

	// Session-scoped defense-in-depth: when the caller supplies session_id in
	// the body, reject a mismatch. Missing session_id is permitted (local
	// single-user deploys).
	if sess, ok := extractSessionID(raw); ok && sess != inst.SessionID {
		a.errorResp(w, http.StatusForbidden, "session mismatch")
		return
	}

	handler := chat.LookupResponseHandler(inst.EnvelopeType)

	ctx, cancel := context.WithTimeout(r.Context(), defaultEnvelopeResponseHandlerTimeout)
	defer cancel()

	result, err := handler.HandleResponse(ctx, *inst, resp)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			_ = a.Services.Store.RecordResponse(envelopeID, "failed", `{"reason":"handler_timeout"}`)
			a.errorResp(w, http.StatusGatewayTimeout, "response handler timed out")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, "handler: "+err.Error())
		return
	}

	respJSON, err := json.Marshal(resp)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "marshal response: "+err.Error())
		return
	}
	if err := a.Services.Store.RecordResponse(envelopeID, string(resp.Status), string(respJSON)); err != nil {
		if errors.Is(err, store.ErrEnvelopeAlreadyResponded) {
			// Someone raced us. Re-read and return 409.
			again, _ := a.Services.Store.GetEnvelopeInstance(envelopeID)
			a.jsonResp(w, http.StatusConflict, map[string]any{
				"error":           "envelope already responded",
				"response_status": again.ResponseStatus,
				"response":        json.RawMessage(again.ResponseJSON),
			})
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := map[string]any{"ok": true}
	if result.FollowUp != "" {
		out["follow_up"] = result.FollowUp
	}

	if !result.Silent {
		payload := result.TranscriptData
		if payload == nil {
			payload = map[string]any{}
		}
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			a.errorResp(w, http.StatusInternalServerError, "marshal payload: "+err.Error())
			return
		}
		msg := &store.Message{
			SessionID: inst.SessionID,
			Role:      chat.RoleEnvelopeResponse,
			Content:   chat.FormatEnvelopeResponseContent(inst.EnvelopeType, resp.Status, string(payloadJSON)),
		}
		if err := a.Services.Store.CreateMessage(msg); err != nil {
			a.errorResp(w, http.StatusInternalServerError, "persist message: "+err.Error())
			return
		}
		out["message_id"] = msg.ID
	}

	slog.Info("envelope respond",
		"envelope_id", envelopeID,
		"type", inst.EnvelopeType,
		"status", resp.Status,
		"silent", result.Silent,
		"session_id", inst.SessionID,
	)

	a.jsonResp(w, http.StatusOK, out)
}

// extractSessionID pulls an optional session_id from the raw JSON body so
// session-scope enforcement doesn't require the ResponseV1 schema to grow a
// new field. Returns ok=false when the field is absent.
func extractSessionID(raw []byte) (string, bool) {
	var probe struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return "", false
	}
	if probe.SessionID == "" {
		return "", false
	}
	return probe.SessionID, true
}
