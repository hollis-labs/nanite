package store

import (
	"database/sql"
	"fmt"

	"github.com/hollis-labs/nanite/internal/reflex"
)

// LogReflexMatch persists one row to playbook_match_log (migration 032).
// It implements reflex.MatchLogger so *Store satisfies that interface and
// can be passed directly to reflex.AssignRoleWithReflex.
//
// Errors are returned so callers can log them; the reflex dispatcher swallows
// them intentionally so a transient DB failure never blocks the dispatch path.
func (s *Store) LogReflexMatch(entry reflex.ReflexMatchEntry) error {
	var turnID sql.NullString
	if entry.TurnID != "" {
		turnID = sql.NullString{String: entry.TurnID, Valid: true}
	}

	_, err := s.DB.Exec(`
		INSERT INTO playbook_match_log
			(session_id, turn_id, reflex_id, priority, source,
			 matched_input_excerpt, hint_tier, hint_pattern, profile_slug, mode)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.SessionID,
		turnID,
		entry.ReflexID,
		entry.Priority,
		entry.Source,
		entry.MatchedInputExcerpt,
		entry.HintTier,
		entry.HintPattern,
		entry.ProfileSlug,
		entry.Mode,
	)
	if err != nil {
		return fmt.Errorf("store: log reflex match: %w", err)
	}
	return nil
}

// ReflexMatchLog is a row read back from playbook_match_log, used in tests
// and future analytics queries.
type ReflexMatchLog struct {
	ID                  int64          `json:"id"`
	SessionID           string         `json:"session_id"`
	TurnID              string         `json:"turn_id,omitempty"`
	ReflexID            string         `json:"reflex_id"`
	Priority            int            `json:"priority"`
	Source              string         `json:"source"`
	MatchedInputExcerpt string         `json:"matched_input_excerpt"`
	HintTier            string         `json:"hint_tier"`
	HintPattern         string         `json:"hint_pattern"`
	ProfileSlug         string         `json:"profile_slug"`
	Mode                string         `json:"mode"`
	MatchedAt           string         `json:"matched_at"`
}

// ListReflexMatchLog returns all rows for a session ordered by id asc.
// Used in tests; future callers may add pagination.
func (s *Store) ListReflexMatchLog(sessionID string) ([]ReflexMatchLog, error) {
	rows, err := s.DB.Query(`
		SELECT id, session_id, COALESCE(turn_id,''), reflex_id, priority, source,
		       matched_input_excerpt, hint_tier, hint_pattern, profile_slug, mode, matched_at
		FROM playbook_match_log
		WHERE session_id = ?
		ORDER BY id ASC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list reflex match log: %w", err)
	}
	defer rows.Close()

	var out []ReflexMatchLog
	for rows.Next() {
		var r ReflexMatchLog
		if err := rows.Scan(
			&r.ID, &r.SessionID, &r.TurnID, &r.ReflexID, &r.Priority, &r.Source,
			&r.MatchedInputExcerpt, &r.HintTier, &r.HintPattern, &r.ProfileSlug, &r.Mode, &r.MatchedAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan reflex match log row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reflex match log rows: %w", err)
	}
	return out, nil
}
