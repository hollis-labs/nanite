package store

import (
	"context"
	"database/sql"
	"fmt"
)

// AgentLogEntry is one row in the agent_log table — an append-only
// per-agent log entry. The id is caller-supplied (ULID or UUID) so the
// caller controls ordering when datetime('now') second-resolution ties.
type AgentLogEntry struct {
	ID        string `json:"id"`
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
	Timestamp string `json:"ts"`
	Kind      string `json:"kind"`
	Entry     string `json:"entry"`
}

const agentLogColumns = `id, agent_id, session_id, ts, kind, entry`

func scanAgentLogEntry(scanner interface{ Scan(...any) error }, e *AgentLogEntry) error {
	return scanner.Scan(&e.ID, &e.AgentID, &e.SessionID, &e.Timestamp, &e.Kind, &e.Entry)
}

// AppendLog inserts a single agent_log row. Append-only — no update or
// delete is offered for this phase. The id is caller-supplied; an empty
// id is an error so callers don't accidentally collide on the PK.
func (s *Store) AppendLog(ctx context.Context, row AgentLogEntry) error {
	if row.ID == "" {
		return fmt.Errorf("append agent_log: id is required")
	}
	if row.AgentID == "" {
		return fmt.Errorf("append agent_log: agent_id is required")
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO agent_log (id, agent_id, session_id, ts, kind, entry)
		 VALUES (?, ?, ?,
		         COALESCE(NULLIF(?, ''), datetime('now')),
		         ?, ?)`,
		row.ID, row.AgentID, row.SessionID, row.Timestamp, row.Kind, row.Entry,
	)
	if err != nil {
		return fmt.Errorf("append agent_log: %w", err)
	}
	return nil
}

// ListLog returns the most-recent agent_log rows for an agent, ordered by
// ts DESC, id DESC. limit ≤ 0 returns every row — this is an unbounded
// scan, callers are expected to pass a sensible bound.
func (s *Store) ListLog(ctx context.Context, agentID string, limit int) ([]AgentLogEntry, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if limit > 0 {
		rows, err = s.DB.QueryContext(ctx,
			`SELECT `+agentLogColumns+`
			 FROM agent_log
			 WHERE agent_id = ?
			 ORDER BY ts DESC, id DESC
			 LIMIT ?`,
			agentID, limit,
		)
	} else {
		rows, err = s.DB.QueryContext(ctx,
			`SELECT `+agentLogColumns+`
			 FROM agent_log
			 WHERE agent_id = ?
			 ORDER BY ts DESC, id DESC`,
			agentID,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list agent_log: %w", err)
	}
	defer rows.Close()

	out := make([]AgentLogEntry, 0)
	for rows.Next() {
		var e AgentLogEntry
		if err := scanAgentLogEntry(rows, &e); err != nil {
			return nil, fmt.Errorf("scan agent_log: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
