package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// handleEnvelopeRespond accepts a typed ResponseV1 payload from the frontend,
// checks it against the path, and records it through EnvelopeService, which
// validates it against the envelope, dispatches to the registered
// ResponseHandler, and persists the response and an envelope_response
// transcript message. It returns the transcript message ID and any
// follow-up. See plans/phase-3-s5-envelope-typed-responses.md §T4.
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

	// Missing session_id is permitted (local single-user deploys);
	// present-but-non-string is treated as a mismatch.
	bodySession, bodySessionPresent := extractSessionID(raw)
	result, err := a.Services.Envelopes.Respond(r.Context(), service.RespondInput{
		EnvelopeID:         envelopeID,
		Response:           resp,
		BodySessionID:      bodySession,
		BodySessionPresent: bodySessionPresent,
	})
	if err != nil {
		var ee *service.EnvelopeError
		if !errors.As(err, &ee) {
			a.errorResp(w, http.StatusInternalServerError, err.Error())
			return
		}
		switch ee.Kind {
		case service.EnvelopeNotFound:
			a.errorResp(w, http.StatusNotFound, ee.Msg)
		case service.EnvelopeInvalid:
			a.errorResp(w, http.StatusBadRequest, ee.Msg)
		case service.EnvelopeForbidden:
			a.errorResp(w, http.StatusForbidden, ee.Msg)
		case service.EnvelopeConflict:
			a.jsonResp(w, http.StatusConflict, map[string]any{
				"error":           ee.Msg,
				"response_status": ee.PriorStatus,
				"response":        json.RawMessage(ee.PriorResponse),
			})
		case service.EnvelopeTimeout:
			a.errorResp(w, http.StatusGatewayTimeout, ee.Msg)
		default:
			a.errorResp(w, http.StatusInternalServerError, ee.Msg)
		}
		return
	}

	out := map[string]any{"ok": true}
	if result.FollowUp != "" {
		out["follow_up"] = result.FollowUp
	}
	if !result.Silent {
		out["message_id"] = result.MessageID
	}

	slog.Info("envelope respond",
		"envelope_id", envelopeID,
		"type", result.EnvelopeType,
		"status", resp.Status,
		"silent", result.Silent,
		"session_id", result.SessionID,
	)

	a.jsonResp(w, http.StatusOK, out)
}

// injectEnvelopePriorResponses post-processes messages returned from the store,
// injectEnvelopePriorResponses post-processes messages returned from the store,
// injecting a "prior_response" key into the envelope JSON of any message whose
// envelope was already responded to. The lookup map is keyed by envelope ID.
// Handles both single-object and JSON-array Envelope fields.
func injectEnvelopePriorResponses(messages []store.Message, lookup map[string]*store.EnvelopeInstance) []store.Message {
	for i, msg := range messages {
		if msg.Envelope == "" {
			continue
		}

		raw := json.RawMessage(msg.Envelope)

		// Detect array vs single-object format.
		trimmed := bytes.TrimLeft(raw, " \t\r\n")
		if len(trimmed) > 0 && trimmed[0] == '[' {
			// Array of envelopes — inject into each element that has a responded id.
			var arr []map[string]any
			if err := json.Unmarshal(raw, &arr); err != nil {
				continue
			}
			changed := false
			for j, env := range arr {
				id, _ := env["id"].(string)
				if id == "" {
					continue
				}
				inst, ok := lookup[id]
				if !ok || inst.ResponseJSON == "" {
					continue
				}
				var resp any
				if err := json.Unmarshal([]byte(inst.ResponseJSON), &resp); err != nil {
					continue
				}
				arr[j]["prior_response"] = resp
				changed = true
			}
			if changed {
				enriched, err := json.Marshal(arr)
				if err == nil {
					messages[i].Envelope = string(enriched)
				}
			}
		} else {
			// Single-object envelope.
			var env map[string]any
			if err := json.Unmarshal(raw, &env); err != nil {
				continue
			}
			id, _ := env["id"].(string)
			if id == "" {
				continue
			}
			inst, ok := lookup[id]
			if !ok || inst.ResponseJSON == "" {
				continue
			}
			var resp any
			if err := json.Unmarshal([]byte(inst.ResponseJSON), &resp); err != nil {
				continue
			}
			env["prior_response"] = resp
			enriched, err := json.Marshal(env)
			if err != nil {
				continue
			}
			messages[i].Envelope = string(enriched)
		}
	}
	return messages
}

// extractSessionID pulls an optional session_id from the raw JSON body so
// session-scope enforcement doesn't require the ResponseV1 schema to grow a
// new field.
//
// Return contract:
//   - (sid, true)  — present, string, non-empty: enforce match.
//   - ("", true)   — present but not a non-empty string (number, object,
//     null, empty string): treat as mismatch so a forged body
//     can't bypass the check by using a wrong JSON type.
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
