package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// PluginReflexSeed binds one validated, explicit-agent default to its durable
// definition. Binding identities remain stable across bundle updates.
type PluginReflexSeed struct {
	SeedID     string
	Definition AgentReflex
}

// BindPluginReflexSeeds creates absent defaults atomically and returns the
// surviving definitions. Existing definitions, edits, history and deletion
// tombstones are never overwritten by defaults.
func (s *Store) BindPluginReflexSeeds(ctx context.Context, owner string, seeds []PluginReflexSeed) ([]AgentReflex, error) {
	if owner == "" || len(seeds) > 64 {
		return nil, errors.New("invalid plugin seed owner or count")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	result := make([]AgentReflex, 0, len(seeds))
	for _, seed := range seeds {
		row := seed.Definition
		if seed.SeedID == "" || row.ID == "" || row.AgentID == "" || row.ClassTag != "" || row.WorkflowRunID != "" || row.ProvenanceTier != "plugin" || row.CreatedBy != "plugin:"+owner || row.ActionKind != ReflexActionInjectReminder || row.TriggerKind != ReflexTriggerPredicate || !row.OptOutAllowed {
			return nil, errors.New("invalid plugin seed binding")
		}
		var bound sql.NullString
		lookupErr := tx.QueryRowContext(ctx, `SELECT reflex_id FROM plugin_reflex_seed_bindings WHERE plugin_id=? AND seed_id=? AND agent_id=?`, owner, seed.SeedID, row.AgentID).Scan(&bound)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			if _, checkErr := tx.ExecContext(ctx, `INSERT INTO agent_reflexes (id,agent_id,name,trigger_kind,trigger_spec,action_kind,action_spec,status,priority,created_by,opt_out_allowed,provenance_tier) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, row.ID, row.AgentID, row.Name, row.TriggerKind, row.TriggerSpec, row.ActionKind, row.ActionSpec, ReflexStatusActive, row.Priority, row.CreatedBy, true, "plugin"); checkErr != nil {
				return nil, fmt.Errorf("create plugin reflex default: %w", checkErr)
			}
			if _, checkErr := tx.ExecContext(ctx, `INSERT INTO plugin_reflex_seed_bindings (plugin_id,seed_id,agent_id,reflex_id) VALUES (?,?,?,?)`, owner, seed.SeedID, row.AgentID, row.ID); checkErr != nil {
				return nil, checkErr
			}
			bound = sql.NullString{String: row.ID, Valid: true}
		} else if lookupErr != nil {
			return nil, lookupErr
		}
		if !bound.Valid {
			continue
		}
		var existing AgentReflex
		if checkErr := scanAgentReflex(tx.QueryRowContext(ctx, `SELECT `+agentReflexColumns+` FROM agent_reflexes WHERE id=?`, bound.String), &existing); checkErr != nil {
			return nil, checkErr
		}
		if existing.CreatedBy != "plugin:"+owner || existing.ProvenanceTier != "plugin" || existing.AgentID != row.AgentID || existing.ClassTag != "" || existing.WorkflowRunID != "" {
			return nil, errors.New("plugin seed binding ownership mismatch")
		}
		result = append(result, existing)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

// SetPluginReflexGate publishes a live-source predicate. It must not access
// the database: candidate enumeration holds the single SQLite connection.
// Catalog/editor reads continue to return inactive plugin definitions.
func (s *Store) SetPluginReflexGate(gate func(AgentReflex) bool) {
	if gate == nil {
		s.pluginReflexGate.Store(nil)
		return
	}
	s.pluginReflexGate.Store(&gate)
}
func (s *Store) pluginReflexAvailable(row AgentReflex) bool {
	if row.ProvenanceTier != "plugin" && !strings.HasPrefix(row.CreatedBy, "plugin:") {
		return true
	}
	gate := s.pluginReflexGate.Load()
	return gate != nil && (*gate)(row)
}
