package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// ErrPinnedContentNotFound is returned when a pinned_content row cannot be located.
var ErrPinnedContentNotFound = errors.New("pinned content not found")

// PinScope constants for the scope column.
const (
	PinScopeTurn         = "turn"          // ephemeral — not persisted, managed in-memory
	PinScopeSession      = "session"       // cleared at session end
	PinScopeCrossSession = "cross_session" // persists until explicit unpin
)

// PinnedContent is a row in the pinned_content table.
// For turn-scoped pins: not stored in DB, managed in-memory by the engine.
// For session-scoped: session_id is set, cleared when session ends.
// For cross_session: session_id is empty, persists until unpin.
type PinnedContent struct {
	ID        string  `json:"id"`
	SessionID *string `json:"session_id,omitempty"` // nil for cross_session
	Scope     string  `json:"scope"`
	Content   string  `json:"content"`
	AgentID   string  `json:"agent_id"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

// CreatePinnedContent inserts a new pinned_content row.
// For cross_session scope, sessionID may be empty.
func (s *Store) CreatePinnedContent(p PinnedContent) error {
	if p.ID == "" {
		return fmt.Errorf("create pinned content: id is required")
	}
	if p.Scope == "" {
		p.Scope = PinScopeSession
	}
	var sessionID interface{}
	if p.SessionID != nil && *p.SessionID != "" {
		sessionID = *p.SessionID
	}
	_, err := s.DB.Exec(
		`INSERT INTO pinned_content (id, session_id, scope, content, agent_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%SZ','now'), strftime('%Y-%m-%dT%H:%M:%SZ','now'))`,
		p.ID, sessionID, p.Scope, p.Content, p.AgentID,
	)
	if err != nil {
		return fmt.Errorf("create pinned content: %w", err)
	}
	return nil
}

// ListPinnedContent returns all pinned items for a session, including
// cross_session items (session_id IS NULL). Items are returned oldest first.
func (s *Store) ListPinnedContent(sessionID string) ([]PinnedContent, error) {
	rows, err := s.DB.Query(
		`SELECT id, session_id, scope, content, agent_id, created_at, updated_at
		 FROM pinned_content
		 WHERE session_id = ? OR scope = ?
		 ORDER BY created_at ASC`,
		sessionID, PinScopeCrossSession,
	)
	if err != nil {
		return nil, fmt.Errorf("list pinned content: %w", err)
	}
	defer rows.Close()
	var out []PinnedContent
	for rows.Next() {
		var p PinnedContent
		var sid sql.NullString
		if err := rows.Scan(&p.ID, &sid, &p.Scope, &p.Content, &p.AgentID, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("list pinned content scan: %w", err)
		}
		if sid.Valid {
			p.SessionID = &sid.String
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeletePinnedContent removes a pinned_content row by ID.
func (s *Store) DeletePinnedContent(id string) error {
	_, err := s.DB.Exec(`DELETE FROM pinned_content WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete pinned content: %w", err)
	}
	return nil
}

// ClearSessionPins removes all session-scoped (non-cross_session) pins for a session.
// Called at session end. Cross-session pins are untouched.
func (s *Store) ClearSessionPins(sessionID string) error {
	_, err := s.DB.Exec(
		`DELETE FROM pinned_content WHERE session_id = ? AND scope = ?`,
		sessionID, PinScopeSession,
	)
	if err != nil {
		return fmt.Errorf("clear session pins: %w", err)
	}
	return nil
}
