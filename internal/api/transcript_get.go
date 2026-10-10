package api

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/hollis-labs/nanite/internal/structuredmessage"
)

// handleGetMessageTranscript returns the exact durable prose for a specific message,
// under the existing operator HTTP history policy. Session consistency is
// checked here; a supplied UUID never becomes verified actor authority.
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
		a.serviceError(w, r, err)
		return
	}

	// Fail if the requested sessionID doesn't match the message's true session.
	if msg.SessionID != sessionID {
		a.errorResp(w, http.StatusForbidden, "message does not belong to the requested session")
		return
	}

	// Decode only the persisted assistant wrapper, keeping the text byte-exact.
	// Other roles retain their literal content, even if it happens to be JSON.
	prose := msg.Content
	if msg.Role == "assistant" {
		if text, wrapped := structuredmessage.UnwrapText(prose); wrapped {
			prose = text
		}
	}
	a.jsonResp(w, http.StatusOK, map[string]string{
		"transcript": prose,
	})
}
