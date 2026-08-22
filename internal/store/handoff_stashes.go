package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrHandoffStashNotFound is returned when a handoff stash row cannot be located.
var ErrHandoffStashNotFound = errors.New("handoff stash not found")

// HandoffStash is a row in the handoff_stashes table. Payload is a JSON blob
// containing the session handoff state (decisions, open questions, file refs, etc.).
type HandoffStash struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Payload   string `json:"payload"`
	CreatedAt string `json:"created_at"`
}

// UpsertHandoffStash inserts or updates a handoff stash row identified by id.
// On conflict the payload and created_at are updated in place.
func (s *Store) UpsertHandoffStash(ctx context.Context, stash HandoffStash) error {
	if stash.ID == "" {
		return fmt.Errorf("upsert handoff stash: id is required")
	}
	if stash.SessionID == "" {
		return fmt.Errorf("upsert handoff stash: session_id is required")
	}
	if stash.CreatedAt == "" {
		return fmt.Errorf("upsert handoff stash: created_at is required")
	}
	if stash.Payload == "" {
		stash.Payload = "{}"
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO handoff_stashes (id, session_id, payload, created_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET payload = excluded.payload, created_at = excluded.created_at`,
		stash.ID, stash.SessionID, stash.Payload, stash.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert handoff stash: %w", err)
	}
	return nil
}

// GetHandoffStash retrieves a specific handoff stash by session_id and id.
// Returns ErrHandoffStashNotFound if no matching row exists.
func (s *Store) GetHandoffStash(ctx context.Context, sessionID, stashID string) (HandoffStash, error) {
	var hs HandoffStash
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, session_id, payload, created_at
		 FROM handoff_stashes WHERE session_id = ? AND id = ?`,
		sessionID, stashID,
	).Scan(&hs.ID, &hs.SessionID, &hs.Payload, &hs.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return HandoffStash{}, ErrHandoffStashNotFound
	}
	if err != nil {
		return HandoffStash{}, fmt.Errorf("get handoff stash: %w", err)
	}
	return hs, nil
}

// GetLatestStashForSession returns the most recent handoff stash for a session
// ordered by created_at DESC. Returns ErrHandoffStashNotFound if none exist.
func (s *Store) GetLatestStashForSession(ctx context.Context, sessionID string) (HandoffStash, error) {
	var hs HandoffStash
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, session_id, payload, created_at
		 FROM handoff_stashes WHERE session_id = ?
		 ORDER BY created_at DESC LIMIT 1`,
		sessionID,
	).Scan(&hs.ID, &hs.SessionID, &hs.Payload, &hs.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return HandoffStash{}, ErrHandoffStashNotFound
	}
	if err != nil {
		return HandoffStash{}, fmt.Errorf("get latest handoff stash: %w", err)
	}
	return hs, nil
}
