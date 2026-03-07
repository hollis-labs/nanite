package store

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Bookmark represents a bookmarked message.
type Bookmark struct {
	ID        string `json:"id"`
	MessageID string `json:"message_id"`
	SessionID string `json:"session_id"`
	Note      string `json:"note"`
	Tags      string `json:"tags"`
	CreatedAt string `json:"created_at"`
}

// ListBookmarks returns all bookmarks for a session.
func (s *Store) ListBookmarks(sessionID string) ([]Bookmark, error) {
	rows, err := s.DB.Query(
		`SELECT id, message_id, session_id, COALESCE(note,''), COALESCE(tags,'[]'), created_at
		 FROM bookmarks WHERE session_id = ?
		 ORDER BY created_at DESC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list bookmarks: %w", err)
	}
	defer rows.Close()

	out := make([]Bookmark, 0)
	for rows.Next() {
		var b Bookmark
		if err := rows.Scan(&b.ID, &b.MessageID, &b.SessionID, &b.Note, &b.Tags, &b.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan bookmark: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// CreateBookmark inserts a new bookmark.
func (s *Store) CreateBookmark(b *Bookmark) error {
	if b.ID == "" {
		b.ID = uuid.New().String()
	}
	if b.Tags == "" {
		b.Tags = "[]"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	b.CreatedAt = now

	_, err := s.DB.Exec(
		`INSERT INTO bookmarks (id, message_id, session_id, note, tags, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		b.ID, b.MessageID, b.SessionID, nullIfEmpty(b.Note), b.Tags, now,
	)
	if err != nil {
		return fmt.Errorf("create bookmark: %w", err)
	}
	return nil
}

// DeleteBookmark removes a bookmark by ID.
func (s *Store) DeleteBookmark(id string) error {
	_, err := s.DB.Exec(`DELETE FROM bookmarks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete bookmark: %w", err)
	}
	return nil
}

// GetBookmarkByMessage returns a bookmark for a specific message, or nil if none exists.
func (s *Store) GetBookmarkByMessage(messageID string) (*Bookmark, error) {
	var b Bookmark
	err := s.DB.QueryRow(
		`SELECT id, message_id, session_id, COALESCE(note,''), COALESCE(tags,'[]'), created_at
		 FROM bookmarks WHERE message_id = ?`,
		messageID,
	).Scan(&b.ID, &b.MessageID, &b.SessionID, &b.Note, &b.Tags, &b.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &b, nil
}
