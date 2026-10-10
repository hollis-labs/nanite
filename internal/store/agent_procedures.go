package store

import (
	"context"
	"errors"
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
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return ErrImmutableAgentProfile
}

func insertAgentProcedure(ctx context.Context, db agentConfigDB, row AgentProcedure) error {
	return ErrImmutableAgentProfile
}

// ListAgentProcedures returns every procedure row for an agent ordered by
// name ASC.
func (s *Store) ListAgentProcedures(ctx context.Context, agentID string) ([]AgentProcedure, error) {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return nil, ErrImmutableAgentProfile
}

// GetAgentProcedure returns a single procedure row by (agent_id, name).
// Returns ErrAgentProcedureNotFound when the row does not exist.
func (s *Store) GetAgentProcedure(ctx context.Context, agentID, name string) (*AgentProcedure, error) {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return nil, ErrImmutableAgentProfile
}

// DeleteAgentProcedure removes a single procedure row. Returns
// ErrAgentProcedureNotFound if no row matched.
func (s *Store) DeleteAgentProcedure(ctx context.Context, agentID, name string) error {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return ErrImmutableAgentProfile
}
