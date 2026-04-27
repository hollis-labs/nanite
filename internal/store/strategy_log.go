package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/nanite/internal/strategy"
)

// LogStrategyDecision persists one strategy decision row to
// strategy_decisions (migration 034). It implements
// strategy.StrategyLogger so *Store satisfies that interface.
//
// Returns the auto-assigned row ID. Errors are returned; callers should
// log and continue — strategy logging must never gate dispatch.
func (s *Store) LogStrategyDecision(entry strategy.DecisionEntry) (int64, error) {
	// Truncate rationale to keep the column bounded; the full reasoning
	// belongs in Vanta when needed for analytics.
	rationale := entry.Rationale
	if len(rationale) > 1024 {
		rationale = rationale[:1024]
	}

	// Serialize grounding consultation IDs as a JSON array. Empty slice
	// becomes "[]" (stable shape for downstream JSON consumers).
	idsJSON, err := json.Marshal(entry.GroundingConsultationIDs)
	if err != nil {
		// Defensive fallback — empty array is safer than failing the
		// log call on a marshal error.
		idsJSON = []byte("[]")
	}

	var turnID sql.NullString
	if entry.TurnID != "" {
		turnID = sql.NullString{String: entry.TurnID, Valid: true}
	}
	var reflexID sql.NullString
	if entry.ReflexMatchID != "" {
		reflexID = sql.NullString{String: entry.ReflexMatchID, Valid: true}
	}
	var playbook sql.NullString
	if entry.PlaybookHit != "" {
		playbook = sql.NullString{String: entry.PlaybookHit, Valid: true}
	}

	res, err := s.DB.Exec(`
		INSERT INTO strategy_decisions
			(session_id, turn_id, approach, rationale, max_turns,
			 escalation_budget, reflex_match_id, playbook_hit,
			 grounding_consultation_ids)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.SessionID,
		turnID,
		string(entry.Approach),
		rationale,
		entry.MaxTurns,
		entry.EscalationBudget,
		reflexID,
		playbook,
		string(idsJSON),
	)
	if err != nil {
		return 0, fmt.Errorf("store: log strategy decision: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: log strategy decision last id: %w", err)
	}
	return id, nil
}

// StrategyDecisionRow is a row read back from strategy_decisions. Used
// in tests and future analytics queries.
type StrategyDecisionRow struct {
	ID                       int64   `json:"id"`
	SessionID                string  `json:"session_id"`
	TurnID                   string  `json:"turn_id,omitempty"`
	Approach                 string  `json:"approach"`
	Rationale                string  `json:"rationale"`
	MaxTurns                 int     `json:"max_turns"`
	EscalationBudget         int     `json:"escalation_budget"`
	ReflexMatchID            string  `json:"reflex_match_id,omitempty"`
	PlaybookHit              string  `json:"playbook_hit,omitempty"`
	GroundingConsultationIDs []int64 `json:"grounding_consultation_ids"`
	CreatedAt                string  `json:"created_at"`
}

// ListStrategyDecisions returns all strategy decision rows for a
// session in id-ascending order. Used by tests; analytics queries can
// build on the same select shape.
func (s *Store) ListStrategyDecisions(sessionID string) ([]StrategyDecisionRow, error) {
	rows, err := s.DB.Query(`
		SELECT id, session_id, COALESCE(turn_id,''), approach, rationale,
		       max_turns, escalation_budget,
		       COALESCE(reflex_match_id,''), COALESCE(playbook_hit,''),
		       grounding_consultation_ids, created_at
		FROM strategy_decisions
		WHERE session_id = ?
		ORDER BY id ASC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list strategy decisions: %w", err)
	}
	defer rows.Close()

	var out []StrategyDecisionRow
	for rows.Next() {
		var r StrategyDecisionRow
		var idsJSON string
		if err := rows.Scan(
			&r.ID, &r.SessionID, &r.TurnID, &r.Approach, &r.Rationale,
			&r.MaxTurns, &r.EscalationBudget,
			&r.ReflexMatchID, &r.PlaybookHit,
			&idsJSON, &r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan strategy decision row: %w", err)
		}
		// Decode the JSON array; tolerate empty/legacy NULLs by
		// substituting an empty slice.
		if idsJSON == "" || idsJSON == "null" {
			r.GroundingConsultationIDs = nil
		} else if err := json.Unmarshal([]byte(idsJSON), &r.GroundingConsultationIDs); err != nil {
			return nil, fmt.Errorf("store: decode strategy decision ids: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: strategy decision rows: %w", err)
	}
	return out, nil
}
