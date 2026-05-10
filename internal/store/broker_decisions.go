package store

// Agent broker decision log — distinct from the tool broker's decision log
// in broker.go. Naming history:
//
//   - The tool broker (request_tools / progressive discovery, CW-20260419-0011)
//     owns the table `broker_decisions` (migration 001 + 031) and the type
//     `BrokerDecision` in broker.go. That schema is intent / layer_reached /
//     selected_tools-shaped.
//   - The agent broker (CW-20260509-0047, SP-20260429-0001 broker-v1) owns the
//     table `agent_broker_decisions` (migration 057) and the type
//     `AgentBrokerDecision` in this file. That schema mirrors the
//     broker.Input + broker.Decision pair from go-agent-broker.
//
// The CW-20260509-0047 boot prompt and decision summary both referred to the
// new schema as `broker_decisions`. That name was already taken upstream by
// the tool broker, so the implementation lands under `agent_broker_decisions`
// and `AgentBrokerDecision` to keep the two telemetry streams unambiguous.
// Sibling tickets CW-20260509-0046 (call-site wiring), CW-20260509-0048 (SSE
// surfacing), and CW-20260509-0049 (admin CLI) consume the *Agent*-prefixed
// API.

import "fmt"

// AgentBrokerDecision is one row in the agent_broker_decisions table.
//
// Columns mirror the broker.Input + broker.Decision pair from
// github.com/hollis-labs/go-agent-broker/broker so a call site can populate
// the struct directly from those values without an intermediate mapping
// layer.
type AgentBrokerDecision struct {
	ID            int64   `json:"id"`
	SessionID     string  `json:"session_id"`
	TurnID        string  `json:"turn_id"`
	UserInputHash string  `json:"user_input_hash"`
	ModeSignal    string  `json:"mode_signal"`
	ScopeTier     string  `json:"scope_tier"`
	ReflexID      string  `json:"reflex_id"`
	Decision      string  `json:"decision"`
	Reason        string  `json:"reason"`
	Confidence    float64 `json:"confidence"`
	CreatedAt     string  `json:"created_at"`
}

// InsertAgentBrokerDecision appends a single agent-broker decision row.
//
// The row argument is taken by pointer so the caller observes the assigned
// auto-increment ID after a successful insert (parity with the Insert
// idiom used elsewhere in this package, e.g. handoff_stashes.go).
//
// Append-only by convention: no Update/Delete helpers exist — the
// agent_broker_decisions table is a telemetry log, not state.
func (s *Store) InsertAgentBrokerDecision(row *AgentBrokerDecision) error {
	if row == nil {
		return fmt.Errorf("InsertAgentBrokerDecision: row is nil")
	}

	res, err := s.DB.Exec(
		`INSERT INTO agent_broker_decisions
		 (session_id, turn_id, user_input_hash, mode_signal, scope_tier,
		  reflex_id, decision, reason, confidence)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.SessionID, row.TurnID, row.UserInputHash, row.ModeSignal,
		row.ScopeTier, row.ReflexID, row.Decision, row.Reason, row.Confidence,
	)
	if err != nil {
		return fmt.Errorf("insert agent_broker_decisions: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("agent_broker_decisions LastInsertId: %w", err)
	}
	row.ID = id

	// Read back created_at so callers (SSE emission per CW-20260509-0048) can
	// surface the server-side timestamp without an extra round-trip.
	if err := s.DB.QueryRow(
		`SELECT created_at FROM agent_broker_decisions WHERE id = ?`,
		id,
	).Scan(&row.CreatedAt); err != nil {
		return fmt.Errorf("read agent_broker_decisions.created_at: %w", err)
	}
	return nil
}

// ListRecentAgentBrokerDecisions returns the most-recent N rows ordered by
// created_at DESC (id DESC tiebreak — id is monotonic per the SQLite rowid
// alias on INTEGER PRIMARY KEY, so this is stable even when rows arrive
// inside the same datetime('now') second).
//
// limit ≤ 0 is treated as a default of 50.
//
// Used by:
//   - CW-20260509-0049 admin CLI (`nanite admin broker-decisions --since N`)
//   - future v2 peer-agent escape hatch telemetry analysis
func (s *Store) ListRecentAgentBrokerDecisions(limit int) ([]*AgentBrokerDecision, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.DB.Query(
		`SELECT id, session_id, turn_id, user_input_hash, mode_signal,
		        scope_tier, reflex_id, decision, reason, confidence, created_at
		 FROM agent_broker_decisions
		 ORDER BY created_at DESC, id DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query agent_broker_decisions: %w", err)
	}
	defer rows.Close()

	out := make([]*AgentBrokerDecision, 0, limit)
	for rows.Next() {
		d := &AgentBrokerDecision{}
		if err := rows.Scan(
			&d.ID, &d.SessionID, &d.TurnID, &d.UserInputHash, &d.ModeSignal,
			&d.ScopeTier, &d.ReflexID, &d.Decision, &d.Reason, &d.Confidence,
			&d.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan agent_broker_decisions: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent_broker_decisions: %w", err)
	}
	return out, nil
}

