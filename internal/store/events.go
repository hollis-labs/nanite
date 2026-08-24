package store

import (
	"context"
	"time"
)

// EventLog represents an operational event for learning and diagnostics.
type EventLog struct {
	ID        int64  `json:"id"`
	SessionID string `json:"session_id,omitempty"`
	EventType string `json:"event_type"` // e.g. "provider_error", "tool_call", "tool_error", "context_budget"
	Category  string `json:"category"`   // e.g. "error", "tool", "context", "performance"
	Detail    string `json:"detail"`
	Metadata  string `json:"metadata"`
	CreatedAt string `json:"created_at"`
}

// LogEvent appends an event to the event log.
func (s *Store) LogEvent(ctx context.Context, sessionID, eventType, category, detail, metadata string) {
	if metadata == "" {
		metadata = "{}"
	}
	_, _ = s.DB.ExecContext(ctx,
		`INSERT INTO event_log (session_id, event_type, category, detail, metadata) VALUES (?, ?, ?, ?, ?)`,
		sessionID, eventType, category, detail, metadata,
	)
}

// ListEvents returns recent events, optionally filtered by category.
func (s *Store) ListEvents(ctx context.Context, category string, limit int) ([]EventLog, error) {
	if limit <= 0 {
		limit = 50
	}

	var query string
	var args []any
	if category != "" {
		query = `SELECT id, COALESCE(session_id,''), event_type, category, COALESCE(detail,''), metadata, created_at FROM event_log WHERE category = ? ORDER BY created_at DESC LIMIT ?`
		args = []any{category, limit}
	} else {
		query = `SELECT id, COALESCE(session_id,''), event_type, category, COALESCE(detail,''), metadata, created_at FROM event_log ORDER BY created_at DESC LIMIT ?`
		args = []any{limit}
	}

	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	out := make([]EventLog, 0)
	for rows.Next() {
		var e EventLog
		var ts time.Time
		if err := rows.Scan(&e.ID, &e.SessionID, &e.EventType, &e.Category, &e.Detail, &e.Metadata, &ts); err != nil {
			return nil, err
		}
		e.CreatedAt = ts.Format(time.RFC3339)
		out = append(out, e)
	}
	return out, nil
}

// CountSessionToolCalls returns the number of tool_call events for a session.
func (s *Store) CountSessionToolCalls(ctx context.Context, sessionID string) int {
	var count int
	_ = s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM event_log WHERE session_id = ? AND event_type = 'tool_call'`,
		sessionID,
	).Scan(&count)
	return count
}
