package store

import (
	"database/sql"
	"fmt"
)

// ReflexMatchLogEntry is the write-shape for LogReflexMatch — one match
// event to persist to playbook_match_log.
//
// Previously internal/promptrouter.ReflexMatchEntry; moved here (Phase 4
// item 03, TASKS/phase-4/03-migrate-promptrouter-to-reflexes.md) when
// internal/promptrouter was retired in full. The table and this write
// path stay alive — the raw-vs-sent audit trail (CW-20260816-0068) is a
// real, still-useful mechanism independent of promptrouter's phrase
// matcher — only the type's package and the field source (now the
// DB-backed dispatch_to_agent reflex evaluation in
// internal/mcp/self_tools_dispatch.go, not internal/promptrouter.Match)
// changed.
type ReflexMatchLogEntry struct {
	SessionID           string
	TurnID              string // optional; empty stored as NULL
	ReflexID            string
	Priority            int
	Source              string // "reflex" for every current writer
	MatchedInputExcerpt string // first 200 chars of raw input
	HintTier            string // classify.ScopeTier.String() at match time
	HintPattern         string // classify.ExecutionPattern.String() at match time
	ProfileSlug         string // the matched reflex's dispatch target agent_slug
	Mode                string // retained for schema/history continuity; no live producer sets this since Modes were cut in full (TASKS/phase-0/21-cut-modes.md)

	// RawInputText and SentInputText (CW-20260816-0068) carry the full,
	// untruncated raw-vs-dispatched text pair for the turn's audit trail.
	// RawInputText is the user's raw input as typed; SentInputText is the
	// text actually sent/dispatched to the spawned agent after any
	// pre-dispatch rewrite (e.g. E2 grounding's memory-block prepend).
	// Callers that perform no rewrite should set both to the same value —
	// the writer (store.LogReflexMatch) collapses identical pairs to empty
	// strings before persisting, so equal values never bloat the table.
	RawInputText  string
	SentInputText string
}

// LogReflexMatch persists one row to playbook_match_log (migration 032).
//
// Errors are returned so callers can log them; the reflex dispatcher swallows
// them intentionally so a transient DB failure never blocks the dispatch path.
func (s *Store) LogReflexMatch(entry ReflexMatchLogEntry) error {
	var turnID sql.NullString
	if entry.TurnID != "" {
		turnID = sql.NullString{String: entry.TurnID, Valid: true}
	}

	// CW-20260816-0068: only persist the full raw/sent text pair when a
	// pre-dispatch rewrite actually changed the dispatched text. The common
	// case is no rewrite (raw == sent), and duplicating the full message
	// body into every logged row in that case would bloat the table for no
	// audit value — matched_input_excerpt already records a 200-char
	// excerpt of the raw input for every match, rewritten or not.
	rawText, sentText := "", ""
	if entry.RawInputText != entry.SentInputText {
		rawText = entry.RawInputText
		sentText = entry.SentInputText
	}

	_, err := s.DB.Exec(`
		INSERT INTO playbook_match_log
			(session_id, turn_id, reflex_id, priority, source,
			 matched_input_excerpt, hint_tier, hint_pattern, profile_slug, mode,
			 raw_input_text, sent_input_text)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
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
		rawText,
		sentText,
	)
	if err != nil {
		return fmt.Errorf("store: log reflex match: %w", err)
	}
	return nil
}

// ReflexMatchLog is a row read back from playbook_match_log, used in tests
// and future analytics queries.
type ReflexMatchLog struct {
	ID                  int64  `json:"id"`
	SessionID           string `json:"session_id"`
	TurnID              string `json:"turn_id,omitempty"`
	ReflexID            string `json:"reflex_id"`
	Priority            int    `json:"priority"`
	Source              string `json:"source"`
	MatchedInputExcerpt string `json:"matched_input_excerpt"`
	HintTier            string `json:"hint_tier"`
	HintPattern         string `json:"hint_pattern"`
	ProfileSlug         string `json:"profile_slug"`
	Mode                string `json:"mode"`
	MatchedAt           string `json:"matched_at"`

	// RawInputText and SentInputText (CW-20260816-0068) are populated only
	// when the writer observed a raw-vs-sent rewrite for this match; both
	// are empty string when the match's raw and sent text were identical.
	RawInputText  string `json:"raw_input_text,omitempty"`
	SentInputText string `json:"sent_input_text,omitempty"`
}

// ListReflexMatchLog returns all rows for a session ordered by id asc.
// Used in tests; future callers may add pagination.
func (s *Store) ListReflexMatchLog(sessionID string) ([]ReflexMatchLog, error) {
	rows, err := s.DB.Query(`
		SELECT id, session_id, COALESCE(turn_id,''), reflex_id, priority, source,
		       matched_input_excerpt, hint_tier, hint_pattern, profile_slug, mode, matched_at,
		       raw_input_text, sent_input_text
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
			&r.RawInputText, &r.SentInputText,
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
