package store

import (
	"database/sql"
	"encoding/json"
)

// BrokerDecision records a tool broker selection event.
//
// Phase 5 (D3, CW-20260419-0011) added the reflection / progressive-discovery
// fields. Older rows carry the column-default values (`outcome="selected"`,
// counters zero) — the schema migration is idempotent ALTER TABLE ADD COLUMN.
type BrokerDecision struct {
	ID               int64    `json:"id"`
	SessionID        string   `json:"session_id"`
	Intent           string   `json:"intent"`
	LayerReached     string   `json:"layer_reached"`
	SelectedTools    []string `json:"selected_tools"`
	Signals          string   `json:"signals"`
	CreatedAt        string   `json:"created_at"`
	ConsecutiveEmpty int      `json:"consecutive_empty"`
	TotalCalls       int      `json:"total_calls"`
	Outcome          string   `json:"outcome"` // selected | loaded | empty | halted | reflected
	LoadedCount      int      `json:"loaded_count"`
	ReflectionQuery  string   `json:"reflection_query,omitempty"`
}

// BrokerDecisionEntry is the input to LogBrokerDecisionEx. Constructed at the
// call site so each caller controls exactly which fields it sets — zero values
// map to the column defaults defined in migration 031.
type BrokerDecisionEntry struct {
	SessionID        string
	Intent           string
	LayerReached     string
	SelectedTools    []string
	Signals          string
	ConsecutiveEmpty int
	TotalCalls       int
	Outcome          string // when "" defaults to "selected"
	LoadedCount      int
	ReflectionQuery  string // optional; "" stored as NULL
}

// LogBrokerDecision persists a broker decision for later analysis.
//
// Compatibility entry point — uses the pre-Phase-5 column set. New call sites
// should use LogBrokerDecisionEx so the reasoning-augmented signals end up in
// the database alongside the selection record.
func (s *Store) LogBrokerDecision(sessionID, intent, layerReached string, selectedTools []string, signals string) error {
	return s.LogBrokerDecisionEx(BrokerDecisionEntry{
		SessionID:     sessionID,
		Intent:        intent,
		LayerReached:  layerReached,
		SelectedTools: selectedTools,
		Signals:       signals,
		Outcome:       "selected",
	})
}

// LogBrokerDecisionEx persists a fully-specified broker decision row.
func (s *Store) LogBrokerDecisionEx(e BrokerDecisionEntry) error {
	toolsJSON, err := json.Marshal(e.SelectedTools)
	if err != nil {
		toolsJSON = []byte("[]")
	}

	outcome := e.Outcome
	if outcome == "" {
		outcome = "selected"
	}

	var refl any
	if e.ReflectionQuery != "" {
		refl = e.ReflectionQuery
	} else {
		refl = nil
	}

	_, err = s.DB.Exec(
		`INSERT INTO broker_decisions
		 (session_id, intent, layer_reached, selected_tools, signals,
		  consecutive_empty, total_calls, outcome, loaded_count, reflection_query)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.SessionID, e.Intent, e.LayerReached, string(toolsJSON), e.Signals,
		e.ConsecutiveEmpty, e.TotalCalls, outcome, e.LoadedCount, refl,
	)
	return err
}

// ListBrokerDecisions returns broker decisions for a session, newest first.
func (s *Store) ListBrokerDecisions(sessionID string, limit int) ([]BrokerDecision, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.DB.Query(
		`SELECT id, session_id, intent, layer_reached, selected_tools, signals, created_at,
		        consecutive_empty, total_calls, outcome, loaded_count, reflection_query
		 FROM broker_decisions WHERE session_id = ? ORDER BY id DESC LIMIT ?`,
		sessionID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var decisions []BrokerDecision
	for rows.Next() {
		var d BrokerDecision
		var toolsJSON string
		var refl sql.NullString
		if err := rows.Scan(
			&d.ID, &d.SessionID, &d.Intent, &d.LayerReached, &toolsJSON, &d.Signals, &d.CreatedAt,
			&d.ConsecutiveEmpty, &d.TotalCalls, &d.Outcome, &d.LoadedCount, &refl,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(toolsJSON), &d.SelectedTools)
		if d.SelectedTools == nil {
			d.SelectedTools = []string{}
		}
		if refl.Valid {
			d.ReflectionQuery = refl.String
		}
		decisions = append(decisions, d)
	}
	return decisions, rows.Err()
}
