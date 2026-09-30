package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/internal/service"
)

func (a *API) handleListBookmarks(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	bookmarks, err := a.Services.Bookmarks.List(r.Context(), sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, bookmarksToView(bookmarks))
}

func (a *API) handleCreateBookmark(w http.ResponseWriter, r *http.Request) {
	var req CreateBookmarkRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.MessageID == "" || req.SessionID == "" {
		a.errorResp(w, http.StatusBadRequest, "message_id and session_id are required")
		return
	}

	b, err := a.Services.Bookmarks.Create(r.Context(), req.MessageID, req.SessionID, req.Note)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Emit plugin event: message bookmarked.
	if a.Services.Plugins != nil {
		safego.Go(r.Context(), "api.bookmarks.emit.bookmarked", func() {
			a.Services.Plugins.EmitMessageBookmarked(b.SessionID, b.MessageID, b.ID)
		})
	}

	a.jsonResp(w, http.StatusCreated, bookmarkToView(b))
}

func (a *API) handleDeleteBookmark(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Look up bookmark before deleting so we can emit the event with context.
	// Any lookup failure is a 404 carrying the store's own error text.
	bookmark, err := a.Services.Bookmarks.Get(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, err.Error())
		return
	}

	if err := a.Services.Bookmarks.Delete(r.Context(), id); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Emit plugin event: message unbookmarked.
	if a.Services.Plugins != nil {
		safego.Go(r.Context(), "api.bookmarks.emit.unbookmarked", func() {
			a.Services.Plugins.EmitMessageUnbookmarked(bookmark.SessionID, bookmark.MessageID, bookmark.ID)
		})
	}

	a.jsonResp(w, http.StatusOK, map[string]string{"deleted": id})
}

func (a *API) handleToggleBookmark(w http.ResponseWriter, r *http.Request) {
	messageID := r.PathValue("id")

	removed, b, err := a.Services.Bookmarks.Toggle(r.Context(), messageID)
	if err != nil {
		if errors.Is(err, service.ErrBookmarkMessageNotFound) {
			a.errorResp(w, http.StatusNotFound, "message not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	if removed {
		// Emit plugin event: message unbookmarked.
		if a.Services.Plugins != nil {
			safego.Go(r.Context(), "api.bookmarks.emit.toggle-unbookmarked", func() {
				a.Services.Plugins.EmitMessageUnbookmarked(b.SessionID, b.MessageID, b.ID)
			})
		}

		a.jsonResp(w, http.StatusOK, map[string]any{
			"action":   "removed",
			"bookmark": bookmarkToView(b),
		})
		return
	}

	// Emit plugin event: message bookmarked.
	if a.Services.Plugins != nil {
		safego.Go(r.Context(), "api.bookmarks.emit.toggle-bookmarked", func() {
			a.Services.Plugins.EmitMessageBookmarked(b.SessionID, b.MessageID, b.ID)
		})
	}

	a.jsonResp(w, http.StatusCreated, map[string]any{
		"action":   "created",
		"bookmark": bookmarkToView(b),
	})
}

func (a *API) handleAutotitleBookmark(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	bookmark, err := a.Services.Bookmarks.Get(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "bookmark not found")
		return
	}

	// Get the bookmarked message content.
	content, err := a.Services.Bookmarks.MessageContent(r.Context(), bookmark)
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
	msgs := []llmtypes.ChatMessage{
		{Role: "user", Content: content},
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	title, err := prov.Complete(ctx, llmtypes.ChatRequest{SystemPrompt: prompt, Messages: msgs, Model: a.Services.UtilityModel})
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "autotitle failed: "+err.Error())
		return
	}

	title = strings.TrimSpace(title)
	if title == "" {
		a.errorResp(w, http.StatusInternalServerError, "autotitle returned empty")
		return
	}

	if err := a.Services.Bookmarks.SetNote(ctx, id, title); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	bookmark.Note = title
	a.jsonResp(w, http.StatusOK, bookmarkToView(bookmark))
}
