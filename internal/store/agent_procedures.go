package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrAgentProcedureNotFound is returned when an agent_procedures row
// cannot be located.
var ErrAgentProcedureNotFound = errors.New("agent procedure not found")

// AgentProcedure is one row in the agent_procedures table — a named
// procedure body recorded against a specific agent. Scope is one of
// "agent" or "shared" today; future scopes can be added without DDL churn
// because the column is plain TEXT.
type AgentProcedure struct {
	AgentID   string `json:"agent_id"`
	Name      string `json:"name"`
	Body      string `json:"body"`
	Scope     string `json:"scope"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

const agentProcedureColumns = `agent_id, name, body, scope, created_at, updated_at`

func scanAgentProcedure(scanner interface{ Scan(...any) error }, p *AgentProcedure) error {
	return scanner.Scan(
		&p.AgentID, &p.Name, &p.Body, &p.Scope, &p.CreatedAt, &p.UpdatedAt,
	)
}

// InsertAgentProcedure upserts a procedure row. PK is (agent_id, name) so
// re-inserting the same pair updates the body / scope and refreshes
// updated_at.
func (s *Store) InsertAgentProcedure(ctx context.Context, row AgentProcedure) error {
	if row.AgentID == "" {
		return fmt.Errorf("insert agent_procedures: agent_id is required")
	}
	if row.Name == "" {
		return fmt.Errorf("insert agent_procedures: name is required")
	}
	if row.Scope == "" {
		row.Scope = "agent"
	}
	// ON CONFLICT lets us preserve the original created_at while bumping
	// updated_at. INSERT OR REPLACE would reset created_at, which is the
	// wrong semantic for a named procedure.
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO agent_procedures (agent_id, name, body, scope, created_at, updated_at)
		 VALUES (?, ?, ?, ?,
		         COALESCE(NULLIF(?, ''), datetime('now')),
		         COALESCE(NULLIF(?, ''), datetime('now')))
		 ON CONFLICT(agent_id, name) DO UPDATE SET
		     body = excluded.body,
		     scope = excluded.scope,
		     updated_at = datetime('now')`,
		row.AgentID, row.Name, row.Body, row.Scope,
		row.CreatedAt, row.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert agent_procedures: %w", err)
	}
	return nil
}

// ListAgentProcedures returns every procedure row for an agent ordered by
// name ASC.
func (s *Store) ListAgentProcedures(ctx context.Context, agentID string) ([]AgentProcedure, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+agentProcedureColumns+`
		 FROM agent_procedures
		 WHERE agent_id = ?
		 ORDER BY name ASC`,
		agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list agent_procedures: %w", err)
	}
	defer rows.Close()

	out := make([]AgentProcedure, 0)
	for rows.Next() {
		var p AgentProcedure
		if err := scanAgentProcedure(rows, &p); err != nil {
			return nil, fmt.Errorf("scan agent_procedures: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetAgentProcedure returns a single procedure row by (agent_id, name).
// Returns ErrAgentProcedureNotFound when the row does not exist.
func (s *Store) GetAgentProcedure(ctx context.Context, agentID, name string) (*AgentProcedure, error) {
	var p AgentProcedure
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+agentProcedureColumns+`
		 FROM agent_procedures
		 WHERE agent_id = ? AND name = ?`,
		agentID, name,
	)
	if err := scanAgentProcedure(row, &p); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAgentProcedureNotFound
		}
		return nil, fmt.Errorf("get agent_procedures: %w", err)
	}
	return &p, nil
}

// DeleteAgentProcedure removes a single procedure row. Returns
// ErrAgentProcedureNotFound if no row matched.
func (s *Store) DeleteAgentProcedure(ctx context.Context, agentID, name string) error {
	res, err := s.DB.ExecContext(ctx,
		`DELETE FROM agent_procedures WHERE agent_id = ? AND name = ?`,
		agentID, name,
	)
	if err != nil {
		return fmt.Errorf("delete agent_procedures: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete agent_procedures rows affected: %w", err)
	}
	if n == 0 {
		return ErrAgentProcedureNotFound
	}
	return nil
}
