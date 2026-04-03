package store

import "encoding/json"

// BrokerDecision records a tool broker selection event.
type BrokerDecision struct {
	ID            int64    `json:"id"`
	SessionID     string   `json:"session_id"`
	Intent        string   `json:"intent"`
	LayerReached  string   `json:"layer_reached"`
	SelectedTools []string `json:"selected_tools"`
	Signals       string   `json:"signals"`
	CreatedAt     string   `json:"created_at"`
}

// LogBrokerDecision persists a broker decision for later analysis.
func (s *Store) LogBrokerDecision(sessionID, intent, layerReached string, selectedTools []string, signals string) error {
	toolsJSON, err := json.Marshal(selectedTools)
	if err != nil {
		toolsJSON = []byte("[]")
	}

	_, err = s.DB.Exec(
		`INSERT INTO broker_decisions (session_id, intent, layer_reached, selected_tools, signals) VALUES (?, ?, ?, ?, ?)`,
		sessionID, intent, layerReached, string(toolsJSON), signals,
	)
	return err
}

// ListBrokerDecisions returns broker decisions for a session, newest first.
func (s *Store) ListBrokerDecisions(sessionID string, limit int) ([]BrokerDecision, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.DB.Query(
		`SELECT id, session_id, intent, layer_reached, selected_tools, signals, created_at
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
		if err := rows.Scan(&d.ID, &d.SessionID, &d.Intent, &d.LayerReached, &toolsJSON, &d.Signals, &d.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(toolsJSON), &d.SelectedTools)
		if d.SelectedTools == nil {
			d.SelectedTools = []string{}
		}
		decisions = append(decisions, d)
	}
	return decisions, rows.Err()
}
