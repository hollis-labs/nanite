package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// ErrReminderNotFound is returned when a reminder row cannot be located.
var ErrReminderNotFound = errors.New("reminder not found")

// Reminder is a row in the reminders table. TriggerJSON is a JSON object
// with one of the v1 shapes:
//
//	{"type":"time","at":"<RFC3339>"}
//	{"type":"turn_count","n":5}
type Reminder struct {
	ID          string  `json:"id"`
	SessionID   string  `json:"session_id"`
	Text        string  `json:"text"`
	TriggerJSON string  `json:"trigger_json"`
	FiredAt     *string `json:"fired_at,omitempty"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

// CreateReminder inserts a new reminder row.
func (s *Store) CreateReminder(r Reminder) error {
	if r.ID == "" {
		return fmt.Errorf("create reminder: id is required")
	}
	if r.SessionID == "" {
		return fmt.Errorf("create reminder: session_id is required")
	}
	if r.TriggerJSON == "" {
		r.TriggerJSON = "{}"
	}
	_, err := s.DB.Exec(
		`INSERT INTO reminders (id, session_id, text, trigger_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%SZ','now'), strftime('%Y-%m-%dT%H:%M:%SZ','now'))`,
		r.ID, r.SessionID, r.Text, r.TriggerJSON,
	)
	if err != nil {
		return fmt.Errorf("create reminder: %w", err)
	}
	return nil
}

// GetReminder fetches one reminder by ID.
func (s *Store) GetReminder(id string) (Reminder, error) {
	var r Reminder
	var firedAt sql.NullString
	err := s.DB.QueryRow(
		`SELECT id, session_id, text, trigger_json, fired_at, created_at, updated_at
		 FROM reminders WHERE id = ?`, id,
	).Scan(&r.ID, &r.SessionID, &r.Text, &r.TriggerJSON, &firedAt, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Reminder{}, ErrReminderNotFound
	}
	if err != nil {
		return Reminder{}, fmt.Errorf("get reminder: %w", err)
	}
	if firedAt.Valid {
		r.FiredAt = &firedAt.String
	}
	return r, nil
}

// ListUnfiredReminders returns all unfired reminders for a session.
func (s *Store) ListUnfiredReminders(sessionID string) ([]Reminder, error) {
	rows, err := s.DB.Query(
		`SELECT id, session_id, text, trigger_json, fired_at, created_at, updated_at
		 FROM reminders WHERE session_id = ? AND fired_at IS NULL
		 ORDER BY created_at ASC`, sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list unfired reminders: %w", err)
	}
	defer rows.Close()
	var out []Reminder
	for rows.Next() {
		var r Reminder
		var firedAt sql.NullString
		if err := rows.Scan(&r.ID, &r.SessionID, &r.Text, &r.TriggerJSON, &firedAt, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("list unfired reminders scan: %w", err)
		}
		if firedAt.Valid {
			r.FiredAt = &firedAt.String
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkReminderFired sets fired_at to now for the given reminder ID.
func (s *Store) MarkReminderFired(id string) error {
	_, err := s.DB.Exec(
		`UPDATE reminders SET fired_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
		 updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		 WHERE id = ? AND fired_at IS NULL`, id,
	)
	if err != nil {
		return fmt.Errorf("mark reminder fired: %w", err)
	}
	return nil
}

// DeleteReminder deletes a reminder by ID.
func (s *Store) DeleteReminder(id string) error {
	_, err := s.DB.Exec(`DELETE FROM reminders WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete reminder: %w", err)
	}
	return nil
}
