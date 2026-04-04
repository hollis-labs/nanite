package api

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/provider"
	"github.com/hollis-labs/nanite/internal/store"
)

func (a *API) handleListBookmarks(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	bookmarks, err := a.Services.Store.ListBookmarks(sessionID)
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
	if err := a.Services.Store.CreateBookmark(b); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, b)
}

func (a *API) handleDeleteBookmark(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Services.Store.DeleteBookmark(id); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"deleted": id})
}

func (a *API) handleToggleBookmark(w http.ResponseWriter, r *http.Request) {
	messageID := r.PathValue("id")

	// Check if bookmark exists for this message.
	existing, err := a.Services.Store.GetBookmarkByMessage(messageID)
	if err != nil && err != sql.ErrNoRows {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	if existing != nil {
		// Delete existing bookmark.
		if err := a.Services.Store.DeleteBookmark(existing.ID); err != nil {
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
	msg, err := a.Services.Store.GetMessage(messageID)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "message not found")
		return
	}

	b := &store.Bookmark{
		MessageID: messageID,
		SessionID: msg.SessionID,
	}
	if err := a.Services.Store.CreateBookmark(b); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, map[string]any{
		"action":   "created",
		"bookmark": b,
	})
}

func (a *API) handleAutotitleBookmark(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	bookmark, err := a.Services.Store.GetBookmark(id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "bookmark not found")
		return
	}

	// Get the bookmarked message content.
	msg, err := a.Services.Store.GetMessage(bookmark.MessageID)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "bookmarked message not found")
		return
	}

	if a.Services.Providers == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "engine not available")
		return
	}

	prov, ok := a.Services.Providers.Get(a.Services.UtilityProvider)
	if !ok {
		a.errorResp(w, http.StatusServiceUnavailable, "utility provider not available")
		return
	}

	prompt := "Generate a concise 3-8 word title for this bookmarked message. Respond with ONLY the title, no quotes or punctuation."
	msgs := []provider.ChatMessage{
		{Role: "user", Content: msg.Content},
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	title, err := prov.Complete(ctx, prompt, msgs, a.Services.UtilityModel)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "autotitle failed: "+err.Error())
		return
	}

	title = strings.TrimSpace(title)
	if title == "" {
		a.errorResp(w, http.StatusInternalServerError, "autotitle returned empty")
		return
	}

	if err := a.Services.Store.UpdateBookmarkNote(id, title); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	bookmark.Note = title
	a.jsonResp(w, http.StatusOK, bookmark)
}
