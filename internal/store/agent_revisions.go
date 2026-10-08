package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrAgentRevisionConflict = errors.New("agent changed since the revision was read")

// AgentRevision is a persisted row snapshot, not a capability/child backup.
// The sequence orders writes even when timestamps coincide. IDs are opaque.
type AgentRevision struct {
	ID           string       `json:"id"`
	Sequence     int64        `json:"sequence"`
	AgentID      string       `json:"agent_id"`
	Operation    string       `json:"operation"`
	RestoredFrom string       `json:"restored_from,omitempty"`
	CreatedAt    string       `json:"created_at"`
	Profile      AgentProfile `json:"profile"`
}

func (s *Store) ListAgentRevisions(ctx context.Context, agentID string, limit, offset int) ([]AgentRevision, error) {
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, fmt.Errorf("invalid agent revision page")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,sequence,agent_id,operation,restored_from,created_at,profile_json
		FROM agent_profile_revisions WHERE agent_id = ? ORDER BY sequence DESC LIMIT ? OFFSET ?`, agentID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	out := make([]AgentRevision, 0)
	for rows.Next() {
		var row AgentRevision
		var raw string
		if err := rows.Scan(&row.ID, &row.Sequence, &row.AgentID, &row.Operation, &row.RestoredFrom, &row.CreatedAt, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &row.Profile); err != nil {
			return nil, fmt.Errorf("decode agent revision: %w", err)
		}
		row.Profile.Revision = row.ID
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) GetAgentRevision(ctx context.Context, agentID, revision string) (*AgentRevision, error) {
	var row AgentRevision
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT id,sequence,agent_id,operation,restored_from,created_at,profile_json
		FROM agent_profile_revisions WHERE agent_id = ? AND id = ?`, agentID, revision).
		Scan(&row.ID, &row.Sequence, &row.AgentID, &row.Operation, &row.RestoredFrom, &row.CreatedAt, &raw)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(raw), &row.Profile); err != nil {
		return nil, fmt.Errorf("decode agent revision: %w", err)
	}
	row.Profile.Revision = row.ID
	return &row, nil
}

// The triggers own recording. This transaction only ensures the returned row
// and token belong to this write, rather than a later concurrent mutation.
func (s *Store) writeAgentRow(ctx context.Context, p *AgentProfile, create bool) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	if create {
		err = createAgent(ctx, tx, p)
	} else {
		err = updateAgent(ctx, tx, p)
	}
	if err != nil {
		return err
	}
	saved, err := getAgent(ctx, tx, p.ID)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	*p = *saved
	return nil
}
