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

	// Kind must match the stored envelope type so response_json and
	// envelope_type stay consistent on the instance row.
	if resp.Kind != inst.EnvelopeType {
		a.errorResp(w, http.StatusBadRequest, "response kind does not match envelope type")
		return
	}

	// Session-scoped defense-in-depth: when the caller supplies session_id in
	// the body, reject a mismatch. Missing session_id is permitted (local
	// single-user deploys). Present-but-non-string is treated as mismatch.
	if sess, ok := extractSessionID(raw); ok && sess != inst.SessionID {
		a.errorResp(w, http.StatusForbidden, "session mismatch")
		return
	}

	// Fast-path the already-responded case for 409 with the prior payload.
	// The atomic claim below is the race-safe barrier; this read is a
	// cooperative early return.
	if inst.RespondedAt != nil {
		a.jsonResp(w, http.StatusConflict, map[string]any{
			"error":           "envelope already responded",
			"response_status": inst.ResponseStatus,
			"response":        json.RawMessage(inst.ResponseJSON),
		})
		return
	}

	// Atomically claim the envelope before invoking the handler. Without
	// this, concurrent submissions would each run the ResponseHandler
	// (duplicating side effects) before one RecordResponse call wins.
	if err := a.Services.Store.ClaimEnvelopeForResponse(envelopeID); err != nil {
		if errors.Is(err, store.ErrEnvelopeAlreadyResponded) {
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

	handler := chat.LookupResponseHandler(inst.EnvelopeType)

	ctx, cancel := context.WithTimeout(r.Context(), defaultEnvelopeResponseHandlerTimeout)
	defer cancel()

	result, err := handler.HandleResponse(ctx, *inst, resp)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			_ = a.Services.Store.UpdateEnvelopeResponse(envelopeID, "failed", `{"reason":"handler_timeout"}`)
			a.errorResp(w, http.StatusGatewayTimeout, "response handler timed out")
			return
		}
		_ = a.Services.Store.UpdateEnvelopeResponse(envelopeID, "failed", `{"reason":"handler_error"}`)
		a.errorResp(w, http.StatusInternalServerError, "handler: "+err.Error())
		return
	}

	respJSON, err := json.Marshal(resp)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "marshal response: "+err.Error())
		return
	}
	if err := a.Services.Store.UpdateEnvelopeResponse(envelopeID, string(resp.Status), string(respJSON)); err != nil {
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
// new field.
//
// Return contract:
//   - (sid, true)  — present, string, non-empty: enforce match.
//   - ("", true)   — present but not a non-empty string (number, object,
//                    null, empty string): treat as mismatch so a forged body
//                    can't bypass the check by using a wrong JSON type.
//   - ("", false)  — absent (or the body isn't a JSON object): no enforcement.
func extractSessionID(raw []byte) (string, bool) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return "", false
	}
	rawSID, ok := probe["session_id"]
	if !ok {
		return "", false
	}
	var sid string
	if err := json.Unmarshal(rawSID, &sid); err != nil {
		return "", true
	}
	if sid == "" {
		return "", true
	}
	return sid, true
}
