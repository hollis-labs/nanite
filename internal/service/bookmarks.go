package service

import (
	"context"
	"database/sql"
	"errors"

	"github.com/hollis-labs/nanite/internal/store"
)

// BookmarkService owns message bookmarks: listing a session's bookmarks,
// creating and deleting them, toggling one on a message, and the reads and
// note write an autotitle needs. Plugin events and the autotitle model call
// stay with the caller.
type BookmarkService struct {
	store *store.Store
}

func NewBookmarkService(st *store.Store) *BookmarkService {
	return &BookmarkService{store: st}
}

// ErrBookmarkMessageNotFound reports a toggle on a message that could not be
// read, so there is no session to bookmark it in.
var ErrBookmarkMessageNotFound = errors.New("message not found")

func (s *BookmarkService) List(ctx context.Context, sessionID string) ([]store.Bookmark, error) {
	return s.store.ListBookmarks(ctx, sessionID)
}

// Get returns the store's error unchanged when the bookmark cannot be read
// (sql.ErrNoRows when absent).
func (s *BookmarkService) Get(ctx context.Context, id string) (*store.Bookmark, error) {
	return s.store.GetBookmark(ctx, id)
}

// Create bookmarks messageID in sessionID and returns the stored bookmark.
func (s *BookmarkService) Create(ctx context.Context, messageID, sessionID, note string) (*store.Bookmark, error) {
	b := &store.Bookmark{MessageID: messageID, SessionID: sessionID, Note: note}
	if err := s.store.CreateBookmark(ctx, b); err != nil {
		return nil, err
	}
	return b, nil
}

func (s *BookmarkService) Delete(ctx context.Context, id string) error {
	return s.store.DeleteBookmark(ctx, id)
}

// Toggle removes the message's bookmark if it has one and otherwise creates
// one in the message's session. removed reports which happened; b is the
// bookmark removed or created. It returns ErrBookmarkMessageNotFound when a
// new bookmark is needed but the message cannot be read.
func (s *BookmarkService) Toggle(ctx context.Context, messageID string) (removed bool, b *store.Bookmark, err error) {
	existing, err := s.store.GetBookmarkByMessage(ctx, messageID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, nil, err
	}
	if existing != nil {
		if err = s.store.DeleteBookmark(ctx, existing.ID); err != nil {
			return false, nil, err
		}
		return true, existing, nil
	}

	msg, err := s.store.GetMessage(ctx, messageID)
	if err != nil {
		return false, nil, ErrBookmarkMessageNotFound
	}
	created, err := s.Create(ctx, messageID, msg.SessionID, "")
	if err != nil {
		return false, nil, err
	}
	return false, created, nil
}

// MessageContent returns the content of the message b bookmarks.
func (s *BookmarkService) MessageContent(ctx context.Context, b *store.Bookmark) (string, error) {
	msg, err := s.store.GetMessage(ctx, b.MessageID)
	if err != nil {
		return "", err
	}
	return msg.Content, nil
}

// SetNote replaces the bookmark's note (its title).
func (s *BookmarkService) SetNote(ctx context.Context, id, note string) error {
	return s.store.UpdateBookmarkNote(ctx, id, note)
}
