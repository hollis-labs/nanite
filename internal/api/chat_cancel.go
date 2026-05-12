package api

import "net/http"

// handleCancelChat handles POST /api/sessions/{id}/chat/cancel.
//
// CW-20260512-0006: backstop for user-initiated stop now that the 5-minute
// parent wall-clock deadline has been removed from chat_generate.go. The
// FE composer's stop button calls this endpoint so the BE actually
// cancels the in-flight LLM stream + tool work instead of just closing
// the SSE on the FE side (which would leave the BE generating tokens
// the user no longer wants).
//
// Behavior:
//   - 200 with {status:"cancelled"} when an active generation was
//     cancelled. The cancel propagates through the registered
//     context.CancelFunc; the generateResponse goroutine observes
//     ctx.Err() on its next loop iteration, persists any partial
//     content, emits a "Stopped: cancelled" status event, and returns.
//   - 404 with {error:"no active generation"} when no generation is
//     registered for the session — idempotent: hitting stop twice is
//     not an error condition the user should see surfaced as failure.
//   - 400 when the session id is missing from the path.
//
// Auth model: piggybacks on the basic-auth middleware that gates all
// /api/* routes (see internal/server/server.go). No additional check is
// applied — the path-bound sessionID is the scope. A caller who can
// reach this endpoint can already send messages to the same session.
func (a *API) handleCancelChat(w http.ResponseWriter, r *http.Request) {
	if a.Services == nil || a.Services.Chat == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "chat service not configured")
		return
	}

	sessionID := r.PathValue("id")
	if sessionID == "" {
		a.errorResp(w, http.StatusBadRequest, "missing session id")
		return
	}

	if !a.Services.Chat.CancelActiveGeneration(sessionID) {
		a.errorResp(w, http.StatusNotFound, "no active generation")
		return
	}

	a.jsonResp(w, http.StatusOK, map[string]string{"status": "cancelled"})
}
