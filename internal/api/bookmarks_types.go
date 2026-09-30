package api

import "github.com/hollis-labs/nanite/internal/store"

// BookmarkView is the API-owned wire shape of a bookmark. Its keys match what
// store.Bookmark used to emit directly, so the wire did not change when the
// row stopped riding onto it; tags stays the stored JSON string.
// TestBookmarkViewJSONKeys pins the key set.
type BookmarkView struct {
	ID        string `json:"id"`
	MessageID string `json:"message_id"`
	SessionID string `json:"session_id"`
	Note      string `json:"note"`
	Tags      string `json:"tags"`
	CreatedAt string `json:"created_at"`
}

func bookmarkToView(b *store.Bookmark) BookmarkView {
	return BookmarkView{
		ID:        b.ID,
		MessageID: b.MessageID,
		SessionID: b.SessionID,
		Note:      b.Note,
		Tags:      b.Tags,
		CreatedAt: b.CreatedAt,
	}
}

// bookmarksToView always returns a non-nil slice: an empty list has always
// been [].
func bookmarksToView(rows []store.Bookmark) []BookmarkView {
	out := make([]BookmarkView, 0, len(rows))
	for i := range rows {
		out = append(out, bookmarkToView(&rows[i]))
	}
	return out
}
