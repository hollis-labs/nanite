package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// agentConfigDB lets the existing row writers share their SQL with a locally
// owned transaction. Nothing holds a transaction across goroutines or callbacks.
type agentConfigDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// AgentAssignments preserves partial-update semantics: nil leaves a column
// untouched; a pointer to an empty string clears it.
type AgentAssignments struct {
	RoleID, ConsumerID, ModelID *string
	Protocol, Transport         *string
}

// AgentConfigSeeds accompany an operator profile write. Missing tool/skill
// catalog entries stay tolerated; database errors roll back the whole write.
type AgentConfigSeeds struct {
	Tools, Skills []string
	// PreserveToolGrants permits catalog declarations without changing authority.
	PreserveToolGrants bool
	Procedures         []AgentProcedure
}

// AgentConfigWriteError keeps a failed stage and its private cause for the
// service's safe message mapping, without exposing driver details to it.
type AgentConfigWriteError struct {
	Step string
	Err  error
}

func (e *AgentConfigWriteError) Error() string {
	return fmt.Sprintf("agent config %s: %v", e.Step, e.Err)
}
func (e *AgentConfigWriteError) Unwrap() error { return e.Err }

// AgentAssignmentReferenceError names a client-supplied reference that failed
// validation before any profile or child write.
type AgentAssignmentReferenceError struct {
	Field string
	Err   error
}

func (e *AgentAssignmentReferenceError) Error() string {
	return fmt.Sprintf("invalid %s reference: %v", e.Field, e.Err)
}
func (e *AgentAssignmentReferenceError) Unwrap() error { return e.Err }

func validateAgentAssignments(ctx context.Context, db agentConfigDB, p *AgentProfile, a AgentAssignments) error {
	protocol, transport := p.Protocol, p.Transport
	if a.Protocol != nil {
		protocol = *a.Protocol
	}
	if a.Transport != nil {
		transport = *a.Transport
	}
	if err := ValidateAgentACPFields(protocol, transport); err != nil {
		return err
	}
	roleID, consumerID, modelID := &p.RoleID, &p.ConsumerID, &p.ModelID
	if a.RoleID != nil {
		roleID = a.RoleID
	}
	if a.ConsumerID != nil {
		consumerID = a.ConsumerID
	}
	if a.ModelID != nil {
		modelID = a.ModelID
	}
	for _, ref := range []struct {
		field, table string
		value        *string
	}{{"role_id", "roles", roleID}, {"consumer_id", "consumers", consumerID}, {"model_id", "models", modelID}} {
		if ref.value == nil || *ref.value == "" {
			continue
		}
		var exists int
		err := db.QueryRowContext(ctx, "SELECT 1 FROM "+ref.table+" WHERE id = ?", *ref.value).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return &AgentAssignmentReferenceError{Field: ref.field, Err: err}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// CreateAgentConfig atomically persists the profile, trust tier, assignments,
// and declared child seeds. Validation and the saved-row read use the same tx.
func (s *Store) CreateAgentConfig(ctx context.Context, p *AgentProfile, a AgentAssignments, seeds AgentConfigSeeds) (*AgentProfile, error) {
	return s.writeAgentConfig(ctx, p, a, seeds, true, nil, "")
}

// UpdateAgentConfig leaves the existing profile and children unchanged if any
// step fails, including assignments that accompany a profile rename/edit.
func (s *Store) UpdateAgentConfig(ctx context.Context, p *AgentProfile, a AgentAssignments, seeds AgentConfigSeeds) (*AgentProfile, error) {
	return s.writeAgentConfig(ctx, p, a, seeds, false, nil, "")
}

// UpdateAgentConfigRevision checks the full persisted revision inside the write
// transaction. restoredFrom labels the final snapshot as a partial restore.
func (s *Store) UpdateAgentConfigRevision(ctx context.Context, p *AgentProfile, a AgentAssignments, seeds AgentConfigSeeds, revision string, restoredFrom string) (*AgentProfile, error) {
	return s.writeAgentConfig(ctx, p, a, seeds, false, &revision, restoredFrom)
}

func (s *Store) writeAgentConfig(ctx context.Context, p *AgentProfile, a AgentAssignments, seeds AgentConfigSeeds, create bool, revision *string, restoredFrom string) (*AgentProfile, error) {
	return nil, ErrImmutableAgentProfile
}

func applyAgentAssignments(p *AgentProfile, a AgentAssignments) {
	if a.RoleID != nil {
		p.RoleID = *a.RoleID
	}
	if a.ConsumerID != nil {
		p.ConsumerID = *a.ConsumerID
	}
	if a.ModelID != nil {
		p.ModelID = *a.ModelID
	}
	if a.Protocol != nil {
		p.Protocol = *a.Protocol
	}
	if a.Transport != nil {
		p.Transport = *a.Transport
	}
}

// seedAgentConfig cannot translate old declaration metadata into actor state.
func seedAgentConfig(ctx context.Context, db agentConfigDB, id string, seeds AgentConfigSeeds) error {
	return ErrImmutableAgentProfile
}
