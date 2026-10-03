package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
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
	Procedures    []AgentProcedure
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
	return s.writeAgentConfig(ctx, p, a, seeds, true)
}

// UpdateAgentConfig leaves the existing profile and children unchanged if any
// step fails, including assignments that accompany a profile rename/edit.
func (s *Store) UpdateAgentConfig(ctx context.Context, p *AgentProfile, a AgentAssignments, seeds AgentConfigSeeds) (*AgentProfile, error) {
	return s.writeAgentConfig(ctx, p, a, seeds, false)
}

func (s *Store) writeAgentConfig(ctx context.Context, p *AgentProfile, a AgentAssignments, seeds AgentConfigSeeds, create bool) (*AgentProfile, error) {
	tx, beginErr := s.DB.BeginTx(ctx, nil)
	if beginErr != nil {
		return nil, &AgentConfigWriteError{Step: "begin", Err: beginErr}
	}
	defer rollbackUnlessCommitted(tx)
	fail := func(step string, err error) (*AgentProfile, error) {
		return nil, &AgentConfigWriteError{Step: step, Err: err}
	}
	if err := validateAgentAssignments(ctx, tx, p, a); err != nil {
		return fail("assignments", err)
	}
	if create {
		if err := createAgent(ctx, tx, p); err != nil {
			return fail("create", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE agent_profiles SET default_trust_tier = 'untrusted' WHERE id = ?`, p.ID); err != nil {
			return fail("trust", err)
		}
	} else if err := updateAgent(ctx, tx, p); err != nil {
		return fail("update", err)
	}
	if err := writeAgentAssignments(ctx, tx, p.ID, a); err != nil {
		return fail("assignments", err)
	}
	if err := seedAgentConfig(ctx, tx, p.ID, seeds); err != nil {
		return fail("seeds", err)
	}
	saved, err := getAgent(ctx, tx, p.ID)
	if err != nil {
		return fail("read", err)
	}
	if err := tx.Commit(); err != nil {
		return fail("commit", err)
	}
	return saved, nil
}

func writeAgentAssignments(ctx context.Context, db agentConfigDB, id string, a AgentAssignments) error {
	if err := updateAgentComposition(ctx, db, id, a.RoleID, a.ConsumerID, a.ModelID); err != nil {
		return err
	}
	return updateAgentACPConfig(ctx, db, id, a.Protocol, a.Transport)
}

func seedAgentConfig(ctx context.Context, db agentConfigDB, id string, seeds AgentConfigSeeds) error {
	for i, name := range seeds.Tools {
		if name == "" {
			continue
		}
		if err := insertAgentKnownTool(ctx, db, AgentKnownTool{AgentID: id, ToolName: name, Pinned: true, SortOrder: int64(i + 1), Reason: "role_seed"}); err != nil {
			return err
		}
		var toolID string
		err := db.QueryRowContext(ctx, `SELECT id FROM known_tools WHERE name = ?`, name).Scan(&toolID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if err := grantAgentTool(ctx, db, id, toolID, "role_seed"); err != nil {
			return err
		}
	}
	for _, slug := range seeds.Skills {
		if slug == "" {
			continue
		}
		var skill string
		err := db.QueryRowContext(ctx, `SELECT slug FROM skills WHERE slug = ?`, slug).Scan(&skill)
		if errors.Is(err, sql.ErrNoRows) {
			slog.Warn("store: seed role skill — no such skill in catalog; skipped", "agent_id", id, "skill", slug)
			continue
		}
		if err != nil {
			return err
		}
		var exists int
		err = db.QueryRowContext(ctx, `SELECT 1 FROM agent_known_skills WHERE agent_id = ? AND skill_name = ?`, id, skill).Scan(&exists)
		if err == nil {
			continue
		} // Preserve existing approval/grant state.
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := insertAgentKnownSkill(ctx, db, AgentKnownSkill{AgentID: id, SkillName: skill, Pinned: true, Reason: "role_seed"}); err != nil {
			return err
		}
	}
	for _, row := range seeds.Procedures {
		if row.Name == "" || row.Body == "" {
			continue
		}
		row.AgentID = id
		if err := insertAgentProcedure(ctx, db, row); err != nil {
			return err
		}
	}
	return nil
}
