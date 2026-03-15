package api

import (
	"database/sql"
	"net/http"

	"github.com/hollis-labs/conduit/internal/store"
)

func (a *API) handleListBookmarks(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	bookmarks, err := a.Store.ListBookmarks(sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if bookmarks == nil {
		bookmarks = []store.Bookmark{}
	}
	a.jsonResp(w, http.StatusOK, bookmarks)
}

func (a *API) handleCreateBookmark(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MessageID string `json:"message_id"`
		SessionID string `json:"session_id"`
		Note      string `json:"note"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.MessageID == "" || req.SessionID == "" {
		a.errorResp(w, http.StatusBadRequest, "message_id and session_id are required")
		return
	}

	b := &store.Bookmark{
		MessageID: req.MessageID,
		SessionID: req.SessionID,
		Note:      req.Note,
	}
	if err := a.Store.CreateBookmark(b); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, b)
}

func (a *API) handleDeleteBookmark(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Store.DeleteBookmark(id); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"deleted": id})
}

func (a *API) handleToggleBookmark(w http.ResponseWriter, r *http.Request) {
	messageID := r.PathValue("id")

	// Check if bookmark exists for this message.
	existing, err := a.Store.GetBookmarkByMessage(messageID)
	if err != nil && err != sql.ErrNoRows {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	if existing != nil {
		// Delete existing bookmark.
		if err := a.Store.DeleteBookmark(existing.ID); err != nil {
			a.errorResp(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.jsonResp(w, http.StatusOK, map[string]any{
			"action":   "removed",
			"bookmark": existing,
		})
		return
	}

	// Need session_id from the message.
	msg, err := a.Store.GetMessage(messageID)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "message not found")
		return
	}

	b := &store.Bookmark{
		MessageID: messageID,
		SessionID: msg.SessionID,
	}
	if err := a.Store.CreateBookmark(b); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, map[string]any{
		"action":   "created",
		"bookmark": b,
	})
}
