package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrPinnedContentNotFound is returned when a pinned_content row cannot be located.
var ErrPinnedContentNotFound = errors.New("pinned content not found")

// PinScope constants for the scope column.
//
// D1 (CW-20260428-0014): the legacy `cross_session` value is replaced
// by `project` cleanly — no compatibility shim, no rows migrated.
const (
	PinScopeTurn    = "turn"    // ephemeral — not persisted, managed in-memory
	PinScopeSession = "session" // cleared at session end
	PinScopeProject = "project" // persists for the project; surfaces in any session of that project
)

// PinnedContent is a row in the pinned_content table.
//
// Scope semantics:
//   - turn    — not stored in DB; managed in-memory by the engine.
//   - session — session_id is set and cleared when session ends.
//   - project — project_id is set; surfaces in any session of that project.
//
// SessionID stays populated for project-scoped pins as well so we retain
// the originating session for provenance/UI display.
type PinnedContent struct {
	ID        string  `json:"id"`
	SessionID *string `json:"session_id,omitempty"`
	Scope     string  `json:"scope"`
	ProjectID string  `json:"project_id,omitempty"`
	Content   string  `json:"content"`
	AgentID   string  `json:"agent_id"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

// CreatePinnedContent inserts a new pinned_content row.
// For project-scoped pins the project_id field is required.
func (s *Store) CreatePinnedContent(ctx context.Context, p PinnedContent) error {
	if p.ID == "" {
		return fmt.Errorf("create pinned content: id is required")
	}
	if p.Scope == "" {
		p.Scope = PinScopeSession
	}
	switch p.Scope {
	case PinScopeTurn, PinScopeSession, PinScopeProject:
	default:
		return fmt.Errorf("create pinned content: invalid scope %q", p.Scope)
	}
	if p.Scope == PinScopeProject && p.ProjectID == "" {
		return fmt.Errorf("create pinned content: project_id is required for scope=project")
	}
	var sessionID interface{}
	if p.SessionID != nil && *p.SessionID != "" {
		sessionID = *p.SessionID
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO pinned_content (id, session_id, scope, project_id, content, agent_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%SZ','now'), strftime('%Y-%m-%dT%H:%M:%SZ','now'))`,
		p.ID, sessionID, p.Scope, nullIfEmpty(p.ProjectID), p.Content, p.AgentID,
	)
	if err != nil {
		return fmt.Errorf("create pinned content: %w", err)
	}
	return nil
}

// ListPinnedContent returns all pinned items for a session, including
// project-scoped items that match the session's project (any session in
// the project surfaces project-scoped pins). Items are returned oldest first.
func (s *Store) ListPinnedContent(ctx context.Context, sessionID string) ([]PinnedContent, error) {
	// Resolve the session's project_id once to drive the project-scoped union.
	var projectID sql.NullString
	if err := s.DB.QueryRowContext(ctx,
		`SELECT project_id FROM sessions WHERE id = ?`, sessionID,
	).Scan(&projectID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("list pinned content: lookup session project: %w", err)
	}

	var rows *sql.Rows
	var err error
	if projectID.Valid && projectID.String != "" {
		rows, err = s.DB.QueryContext(ctx,
			`SELECT id, session_id, scope, project_id, content, agent_id, created_at, updated_at
			 FROM pinned_content
			 WHERE (scope = ? AND session_id = ?)
			    OR (scope = ? AND project_id = ?)
			 ORDER BY created_at ASC`,
			PinScopeSession, sessionID, PinScopeProject, projectID.String,
		)
	} else {
		rows, err = s.DB.QueryContext(ctx,
			`SELECT id, session_id, scope, project_id, content, agent_id, created_at, updated_at
			 FROM pinned_content
			 WHERE scope = ? AND session_id = ?
			 ORDER BY created_at ASC`,
			PinScopeSession, sessionID,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list pinned content: %w", err)
	}
	defer rows.Close()
	var out []PinnedContent
	for rows.Next() {
		var p PinnedContent
		var sid sql.NullString
		var pid sql.NullString
		if err := rows.Scan(&p.ID, &sid, &p.Scope, &pid, &p.Content, &p.AgentID, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("list pinned content scan: %w", err)
		}
		if sid.Valid {
			p.SessionID = &sid.String
		}
		if pid.Valid {
			p.ProjectID = pid.String
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeletePinnedContent removes a pinned_content row by ID.
func (s *Store) DeletePinnedContent(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM pinned_content WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete pinned content: %w", err)
	}
	return nil
}

// UpdatePinScope changes the scope (and optional project_id) for an existing pin.
// Used by the FE promote/demote actions (D2). Validates project-scope invariants.
func (s *Store) UpdatePinScope(ctx context.Context, id, scope, projectID string) error {
	switch scope {
	case PinScopeSession, PinScopeProject:
	default:
		return fmt.Errorf("update pin scope: invalid scope %q (turn pins are ephemeral)", scope)
	}
	if scope == PinScopeProject && projectID == "" {
		return fmt.Errorf("update pin scope: project_id is required for scope=project")
	}
	_, err := s.DB.ExecContext(ctx,
		`UPDATE pinned_content SET scope = ?, project_id = ?,
		 updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		 WHERE id = ?`,
		scope, nullIfEmpty(projectID), id,
	)
	if err != nil {
		return fmt.Errorf("update pin scope: %w", err)
	}
	return nil
}

// ClearSessionPins removes all session-scoped (non-project) pins for a session.
// Called at session end. Project-scoped pins are untouched.
func (s *Store) ClearSessionPins(ctx context.Context, sessionID string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM pinned_content WHERE session_id = ? AND scope = ?`,
		sessionID, PinScopeSession,
	)
	if err != nil {
		return fmt.Errorf("clear session pins: %w", err)
	}
	return nil
}
