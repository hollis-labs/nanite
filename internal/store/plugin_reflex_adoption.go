package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ExistingPluginReflexSeed names a host-selected system definition to transfer.
// It never carries replacement trigger/action text or plugin-selected SQL.
type ExistingPluginReflexSeed struct{ SeedID, AgentID, LegacyName string }

// BindExistingPluginReflexSeeds atomically changes ownership and installs stable
// bindings. Definitions keep their IDs, names, edits, history and opt-outs.
// Missing source definitions become tombstones rather than recreated defaults.
// Call only after all accepted manifest registrations have succeeded.
func (s *Store) BindExistingPluginReflexSeeds(ctx context.Context, owner string, seeds []ExistingPluginReflexSeed) ([]AgentReflex, error) {
	if owner == "" || len(seeds) == 0 || len(seeds) > 64 {
		return nil, errors.New("invalid existing plugin seed owner or count")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	result := make([]AgentReflex, 0, len(seeds))
	for _, seed := range seeds {
		if seed.SeedID == "" || seed.AgentID == "" || seed.LegacyName == "" {
			return nil, errors.New("invalid existing plugin seed binding")
		}
		var bound sql.NullString
		lookupErr := tx.QueryRowContext(ctx, `SELECT reflex_id FROM plugin_reflex_seed_bindings WHERE plugin_id=? AND seed_id=? AND agent_id=?`, owner, seed.SeedID, seed.AgentID).Scan(&bound)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			rows, queryErr := tx.QueryContext(ctx, `SELECT `+agentReflexColumns+` FROM agent_reflexes WHERE agent_id=? AND name=? ORDER BY id LIMIT 2`, seed.AgentID, seed.LegacyName)
			if queryErr != nil {
				return nil, queryErr
			}
			var candidates []AgentReflex
			for rows.Next() {
				var candidate AgentReflex
				if scanErr := scanAgentReflex(rows, &candidate); scanErr != nil {
					_ = rows.Close()
					return nil, scanErr
				}
				candidates = append(candidates, candidate)
			}
			rowsErr := rows.Err()
			_ = rows.Close()
			if rowsErr != nil {
				return nil, rowsErr
			}
			if len(candidates) > 1 {
				return nil, errors.New("ambiguous existing plugin seed source")
			}
			if len(candidates) == 1 {
				row := candidates[0]
				if row.CreatedBy != "system" || row.ProvenanceTier != "system" || row.ClassTag != "" || row.WorkflowRunID != "" || row.TriggerKind != ReflexTriggerPredicate || row.ActionKind != ReflexActionInjectReminder || !row.OptOutAllowed {
					return nil, fmt.Errorf("existing seed %q cannot transfer to plugin provenance", seed.LegacyName)
				}
				if _, updateErr := tx.ExecContext(ctx, `UPDATE agent_reflexes SET created_by=?,provenance_tier='plugin' WHERE id=?`, "plugin:"+owner, row.ID); updateErr != nil {
					return nil, updateErr
				}
				bound = sql.NullString{String: row.ID, Valid: true}
			}
			if _, insertErr := tx.ExecContext(ctx, `INSERT INTO plugin_reflex_seed_bindings(plugin_id,seed_id,agent_id,reflex_id) VALUES(?,?,?,?)`, owner, seed.SeedID, seed.AgentID, bound); insertErr != nil {
				return nil, insertErr
			}
		} else if lookupErr != nil {
			return nil, lookupErr
		}
		if !bound.Valid {
			continue
		}
		var existing AgentReflex
		if scanErr := scanAgentReflex(tx.QueryRowContext(ctx, `SELECT `+agentReflexColumns+` FROM agent_reflexes WHERE id=?`, bound.String), &existing); scanErr != nil {
			return nil, scanErr
		}
		if existing.CreatedBy != "plugin:"+owner || existing.ProvenanceTier != "plugin" || existing.AgentID != seed.AgentID || existing.ClassTag != "" || existing.WorkflowRunID != "" {
			return nil, errors.New("existing plugin seed binding ownership mismatch")
		}
		result = append(result, existing)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
