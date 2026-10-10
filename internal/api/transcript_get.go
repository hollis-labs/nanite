package api

import (
	"database/sql"
	"errors"
	"net/http"
)

// handleGetMessageTranscript returns the exact durable prose for a specific message,
// ensuring the caller has provided the correct sessionID that owns the message.
func (a *API) handleGetMessageTranscript(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	messageID := r.PathValue("messageId")

	if sessionID == "" || messageID == "" {
		a.errorResp(w, http.StatusBadRequest, "session ID and message ID are required")
		return
	}

	msg, err := a.Services.Sessions.GetMessage(r.Context(), messageID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			a.errorResp(w, http.StatusNotFound, "message not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Fail if the requested sessionID doesn't match the message's true session.
	if msg.SessionID != sessionID {
		a.errorResp(w, http.StatusForbidden, "message does not belong to the requested session")
		return
	}

	// Exact durable prose returned without UI or default-rendering mutation.
	a.jsonResp(w, http.StatusOK, map[string]string{
		"transcript": msg.Content,
	})
}
