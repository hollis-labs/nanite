package store

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/grounding"
)

// LogGroundingConsultation persists one row to grounding_consultations
// (migration 033). It implements part of grounding.ConsultationLogger so
// *Store satisfies that interface.
//
// Returns the auto-assigned row ID so outcome write-back can reference it.
// Errors are returned; callers should log and continue — grounding logging
// must never gate the dispatch path.
func (s *Store) LogGroundingConsultation(entry grounding.ConsultationEntry) (int64, error) {
	consumed := 0
	if entry.Consumed {
		consumed = 1
	}

	// Truncate summary to 512 chars to match the column intent in the
	// migration comment. Full body is in Vanta; the log only needs the summary.
	summary := entry.Summary
	if len(summary) > 512 {
		summary = summary[:512]
	}

	var turnID sql.NullString
	if entry.TurnID != "" {
		turnID = sql.NullString{String: entry.TurnID, Valid: true}
	}

	res, err := s.DB.Exec(`
		INSERT INTO grounding_consultations
			(session_id, turn_id, memory_key, namespace, summary, similarity, consumed)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		entry.SessionID,
		turnID,
		entry.MemoryKey,
		entry.Namespace,
		summary,
		entry.Similarity,
		consumed,
	)
	if err != nil {
		return 0, fmt.Errorf("store: log grounding consultation: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: log grounding consultation last id: %w", err)
	}
	return id, nil
}

// LogGroundingOutcome persists one row to grounding_outcomes (migration 033).
// It implements part of grounding.ConsultationLogger so *Store satisfies that
// interface.
func (s *Store) LogGroundingOutcome(outcome grounding.Outcome) error {
	pruning := strings.Join(outcome.PruningWordsFound, ",")

	_, err := s.DB.Exec(`
		INSERT INTO grounding_outcomes
			(consultation_id, outcome_kind, follow_up_excerpt, seconds_since_ack, pruning_words)
		VALUES (?, ?, ?, ?, ?)`,
		outcome.ConsultationID,
		string(outcome.Kind),
		outcome.FollowUpExcerpt,
		outcome.SecondsSinceAck,
		pruning,
	)
	if err != nil {
		return fmt.Errorf("store: log grounding outcome: %w", err)
	}
	return nil
}

// GroundingConsultationRow is a row read back from grounding_consultations.
// Used in tests and future analytics queries.
type GroundingConsultationRow struct {
	ID          int64   `json:"id"`
	SessionID   string  `json:"session_id"`
	TurnID      string  `json:"turn_id,omitempty"`
	MemoryKey   string  `json:"memory_key"`
	Namespace   string  `json:"namespace"`
	Summary     string  `json:"summary"`
	Similarity  float64 `json:"similarity"`
	Consumed    bool    `json:"consumed"`
	ConsultedAt string  `json:"consulted_at"`
}

// ListGroundingConsultations returns all rows for a session ordered by id asc.
// Used in tests.
func (s *Store) ListGroundingConsultations(sessionID string) ([]GroundingConsultationRow, error) {
	rows, err := s.DB.Query(`
		SELECT id, session_id, COALESCE(turn_id,''), memory_key, namespace, summary,
		       similarity, consumed, consulted_at
		FROM grounding_consultations
		WHERE session_id = ?
		ORDER BY id ASC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list grounding consultations: %w", err)
	}
	defer rows.Close()

	var out []GroundingConsultationRow
	for rows.Next() {
		var r GroundingConsultationRow
		var consumed int
		if err := rows.Scan(
			&r.ID, &r.SessionID, &r.TurnID, &r.MemoryKey, &r.Namespace, &r.Summary,
			&r.Similarity, &consumed, &r.ConsultedAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan grounding consultation row: %w", err)
		}
		r.Consumed = consumed == 1
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: grounding consultations rows: %w", err)
	}
	return out, nil
}

// GroundingOutcomeRow is a row read back from grounding_outcomes.
// Used in tests and future analytics queries.
type GroundingOutcomeRow struct {
	ID               int64   `json:"id"`
	ConsultationID   int64   `json:"consultation_id"`
	OutcomeKind      string  `json:"outcome_kind"`
	FollowUpExcerpt  string  `json:"follow_up_excerpt"`
	SecondsSinceAck  float64 `json:"seconds_since_ack"`
	PruningWords     string  `json:"pruning_words"`
	RecordedAt       string  `json:"recorded_at"`
}

// ListGroundingOutcomes returns all outcome rows for a consultation ID.
// Used in tests.
func (s *Store) ListGroundingOutcomes(consultationID int64) ([]GroundingOutcomeRow, error) {
	rows, err := s.DB.Query(`
		SELECT id, consultation_id, outcome_kind, follow_up_excerpt,
		       seconds_since_ack, pruning_words, recorded_at
		FROM grounding_outcomes
		WHERE consultation_id = ?
		ORDER BY id ASC`,
		consultationID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list grounding outcomes: %w", err)
	}
	defer rows.Close()

	var out []GroundingOutcomeRow
	for rows.Next() {
		var r GroundingOutcomeRow
		if err := rows.Scan(
			&r.ID, &r.ConsultationID, &r.OutcomeKind, &r.FollowUpExcerpt,
			&r.SecondsSinceAck, &r.PruningWords, &r.RecordedAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan grounding outcome row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: grounding outcome rows: %w", err)
	}
	return out, nil
}
