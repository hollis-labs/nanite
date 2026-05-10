package api

import (
	"net/http"
)

// CancelRecoveryRetryRequest is the body for
// POST /api/sessions/{id}/recovery/cancel. The token comes from the
// info-card envelope wrap (`cancel_token` sibling of id/type/data) the
// recovery broker emitted; the FE round-trips it verbatim.
type CancelRecoveryRetryRequest struct {
	Token string `json:"token"`
}

// handleCancelRecoveryRetry handles POST /api/sessions/{id}/recovery/cancel.
// The endpoint resolves the in-process recovery broker and asks it to
// cancel an in-flight retry identified by token. The broker validates
// that the token was issued for the path-bound sessionID — replays
// from another session are rejected.
//
// Auth model: this endpoint piggybacks on the same basic-auth middleware
// that gates every /api/* handler (see internal/server/server.go's
// middleware chain). No additional check is applied because:
//
//   - The token itself is opaque and unguessable (16 random bytes hex-
//     encoded; see recovery.newCancelToken).
//   - The broker enforces session-scoped validation: a token from
//     session A cannot cancel session B's retry even if the FE made the
//     request through the wrong path-bound URL.
//
// Returns 200 with {status:"cancelled"} on success, 404 when the token
// is unknown / expired / scoped to another session, 400 on missing
// token, and 503 when the broker is not wired (degraded boot, test
// fakes that supply mock RecoveryHooks).
func (a *API) handleCancelRecoveryRetry(w http.ResponseWriter, r *http.Request) {
	if a.Services.Recovery == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "recovery broker not configured")
		return
	}

	sessionID := r.PathValue("id")
	if sessionID == "" {
		a.errorResp(w, http.StatusBadRequest, "missing session id")
		return
	}

	var req CancelRecoveryRetryRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Token == "" {
		a.errorResp(w, http.StatusBadRequest, "missing token")
		return
	}

	if !a.Services.Recovery.Cancel(sessionID, req.Token) {
		a.errorResp(w, http.StatusNotFound, "no active retry for token")
		return
	}

	a.jsonResp(w, http.StatusOK, map[string]string{"status": "cancelled"})
}
