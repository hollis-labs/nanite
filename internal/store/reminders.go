package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrReminderNotFound is returned when a reminder row cannot be located.
var ErrReminderNotFound = errors.New("reminder not found")

// Reminder scope constants (D1, CW-20260428-0014).
//
// Every reminder retains its originating SessionID for provenance even
// when its scope is project-wide; firing semantics are governed by
// the scope column rather than session_id alone.
const (
	ReminderScopeTurn    = "turn"    // fires within the same turn it was created in
	ReminderScopeSession = "session" // default — fires only in originating session
	ReminderScopeProject = "project" // fires in any session of the same project
)

// Reminder is a row in the reminders table. TriggerJSON is a JSON object
// with one of the v1 shapes:
//
//	{"type":"time","at":"<RFC3339>"}
//	{"type":"turn_count","n":5}
//
// Scope (D1, CW-20260428-0014):
//   - "turn"    — fires within the originating turn only.
//   - "session" — fires only in the originating session (default).
//   - "project" — fires in any session of the same project; ProjectID is required.
type Reminder struct {
	ID          string  `json:"id"`
	SessionID   string  `json:"session_id"`
	Scope       string  `json:"scope"`
	ProjectID   string  `json:"project_id,omitempty"`
	Text        string  `json:"text"`
	TriggerJSON string  `json:"trigger_json"`
	FiredAt     *string `json:"fired_at,omitempty"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

// CreateReminder inserts a new reminder row.
func (s *Store) CreateReminder(ctx context.Context, r Reminder) error {
	if r.ID == "" {
		return fmt.Errorf("create reminder: id is required")
	}
	if r.SessionID == "" {
		return fmt.Errorf("create reminder: session_id is required")
	}
	if r.Scope == "" {
		r.Scope = ReminderScopeSession
	}
	switch r.Scope {
	case ReminderScopeTurn, ReminderScopeSession, ReminderScopeProject:
	default:
		return fmt.Errorf("create reminder: invalid scope %q", r.Scope)
	}
	if r.Scope == ReminderScopeProject && r.ProjectID == "" {
		return fmt.Errorf("create reminder: project_id is required for scope=project")
	}
	if r.TriggerJSON == "" {
		r.TriggerJSON = "{}"
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO reminders (id, session_id, scope, project_id, text, trigger_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%SZ','now'), strftime('%Y-%m-%dT%H:%M:%SZ','now'))`,
		r.ID, r.SessionID, r.Scope, nullIfEmpty(r.ProjectID), r.Text, r.TriggerJSON,
	)
	if err != nil {
		return fmt.Errorf("create reminder: %w", err)
	}
	return nil
}

// GetReminder fetches one reminder by ID.
func (s *Store) GetReminder(ctx context.Context, id string) (Reminder, error) {
	var r Reminder
	var firedAt sql.NullString
	var projectID sql.NullString
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, session_id, scope, project_id, text, trigger_json, fired_at, created_at, updated_at
		 FROM reminders WHERE id = ?`, id,
	).Scan(&r.ID, &r.SessionID, &r.Scope, &projectID, &r.Text, &r.TriggerJSON, &firedAt, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Reminder{}, ErrReminderNotFound
	}
	if err != nil {
		return Reminder{}, fmt.Errorf("get reminder: %w", err)
	}
	if firedAt.Valid {
		r.FiredAt = &firedAt.String
	}
	if projectID.Valid {
		r.ProjectID = projectID.String
	}
	return r, nil
}

// ListUnfiredReminders returns all unfired reminders for a session, including
// project-scoped reminders attached to the session's project. The session ID
// is mapped to its project_id at query time so any session in the project sees
// project-scoped reminders set elsewhere.
func (s *Store) ListUnfiredReminders(ctx context.Context, sessionID string) ([]Reminder, error) {
	// Resolve the session's project ID, if any. A session without a project
	// only sees its own session-scoped reminders.
	var projectID sql.NullString
	if err := s.DB.QueryRowContext(ctx,
		`SELECT project_id FROM sessions WHERE id = ?`, sessionID,
	).Scan(&projectID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("list unfired reminders: lookup session project: %w", err)
	}

	var rows *sql.Rows
	var err error
	if projectID.Valid && projectID.String != "" {
		rows, err = s.DB.QueryContext(ctx,
			`SELECT id, session_id, scope, project_id, text, trigger_json, fired_at, created_at, updated_at
			 FROM reminders
			 WHERE fired_at IS NULL
			   AND (
			        (scope IN ('turn','session') AND session_id = ?)
			     OR (scope = 'project' AND project_id = ?)
			   )
			 ORDER BY created_at ASC`,
			sessionID, projectID.String,
		)
	} else {
		rows, err = s.DB.QueryContext(ctx,
			`SELECT id, session_id, scope, project_id, text, trigger_json, fired_at, created_at, updated_at
			 FROM reminders
			 WHERE fired_at IS NULL
			   AND scope IN ('turn','session')
			   AND session_id = ?
			 ORDER BY created_at ASC`, sessionID,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list unfired reminders: %w", err)
	}
	defer rows.Close()
	var out []Reminder
	for rows.Next() {
		var r Reminder
		var firedAt sql.NullString
		var pid sql.NullString
		if err := rows.Scan(&r.ID, &r.SessionID, &r.Scope, &pid, &r.Text, &r.TriggerJSON, &firedAt, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("list unfired reminders scan: %w", err)
		}
		if firedAt.Valid {
			r.FiredAt = &firedAt.String
		}
		if pid.Valid {
			r.ProjectID = pid.String
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkReminderFired sets fired_at to now for the given reminder ID.
func (s *Store) MarkReminderFired(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE reminders SET fired_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
		 updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		 WHERE id = ? AND fired_at IS NULL`, id,
	)
	if err != nil {
		return fmt.Errorf("mark reminder fired: %w", err)
	}
	return nil
}

// UpdateReminderScope changes the scope (and optional project_id) for an
// existing reminder. Used by the FE promote/demote actions (D2). Validates
// that project scope carries a project_id.
func (s *Store) UpdateReminderScope(ctx context.Context, id, scope, projectID string) error {
	switch scope {
	case ReminderScopeTurn, ReminderScopeSession, ReminderScopeProject:
	default:
		return fmt.Errorf("update reminder scope: invalid scope %q", scope)
	}
	if scope == ReminderScopeProject && projectID == "" {
		return fmt.Errorf("update reminder scope: project_id is required for scope=project")
	}
	_, err := s.DB.ExecContext(ctx,
		`UPDATE reminders SET scope = ?, project_id = ?,
		 updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		 WHERE id = ?`,
		scope, nullIfEmpty(projectID), id,
	)
	if err != nil {
		return fmt.Errorf("update reminder scope: %w", err)
	}
	return nil
}

// DeleteReminder deletes a reminder by ID.
func (s *Store) DeleteReminder(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM reminders WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete reminder: %w", err)
	}
	return nil
}
