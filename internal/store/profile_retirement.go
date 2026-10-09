package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	ErrProfileRetirementProtected = errors.New("profile is not editable")
	ErrProfileRetirementSchema    = errors.New("profile export dependency schema is unsupported")
	ErrProfileRetirementConflict  = errors.New("profile state changed after export")
	ErrProfileRetirementBound     = errors.New("profile export exceeds its bound")
)

const ProfileExportMaxBytes = 16 << 20
const profileExportMaxRows = 10000

// ProfileRetirementExport contains a consistent, private export of the profile
// and its affected relational records. It is data, never an import or grant.
type ProfileRetirementExport struct {
	SchemaVersion int                           `json:"schema_version"`
	ProfileID     string                        `json:"profile_id"`
	Slug          string                        `json:"slug"`
	Revision      string                        `json:"revision"`
	Tables        map[string]ProfileExportTable `json:"tables"`
}

type ProfileExportTable struct {
	Columns []string          `json:"columns"`
	Rows    []json.RawMessage `json:"rows"`
}

func (e ProfileRetirementExport) Digest() (string, error) {
	data, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	if len(data) > ProfileExportMaxBytes {
		return "", ErrProfileRetirementBound
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// ExportProfileRetirement snapshots one explicitly selected editable profile.
func (s *Store) ExportProfileRetirement(ctx context.Context, id string) (ProfileRetirementExport, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ProfileRetirementExport{}, err
	}
	defer rollbackUnlessCommitted(tx)
	snapshot, err := profileRetirementSnapshot(ctx, tx, id)
	if err != nil {
		return ProfileRetirementExport{}, err
	}
	return snapshot, tx.Commit()
}

// RetireExportedProfile compares the complete export and applies the existing
// cleanup in the same write transaction. Profile revisions alone do not cover
// mutable grants, schedules and other children.
func (s *Store) RetireExportedProfile(ctx context.Context, id, digest string) error {
	if id == "" || digest == "" {
		return ErrProfileRetirementConflict
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	snapshot, err := profileRetirementSnapshot(ctx, tx, id)
	if err != nil {
		return err
	}
	actual, err := snapshot.Digest()
	if err != nil {
		return err
	}
	if actual != digest {
		return ErrProfileRetirementConflict
	}
	profile, err := getAgent(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := deleteAgentTx(ctx, tx, profile); err != nil {
		return err
	}
	return tx.Commit()
}

func retirementEditable(p *AgentProfile) bool {
	if p == nil || p.PluginID != "" {
		return false
	}
	switch p.Source {
	case "", "api", "cli", "managed_file", "nanite", "project", "user":
		return true
	default:
		return false
	}
}

type retirementLink struct {
	child, parent, from, to string
	unsupported             bool
}

func quoteRetirementName(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

func profileRetirementSnapshot(ctx context.Context, tx *sql.Tx, id string) (ProfileRetirementExport, error) {
	profile, err := getAgent(ctx, tx, id)
	if err != nil {
		return ProfileRetirementExport{}, err
	}
	if !retirementEditable(profile) {
		return ProfileRetirementExport{}, ErrProfileRetirementProtected
	}
	out := ProfileRetirementExport{SchemaVersion: 1, ProfileID: profile.ID, Slug: profile.Slug, Revision: profile.Revision, Tables: map[string]ProfileExportTable{}}
	// Discover declared children so cascading records are exported as well as
	// the explicit cleanup list. Schema identifiers come only from SQLite.
	rows, err := tx.QueryContext(ctx, "SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		return out, err
	}
	var names []string
	for rows.Next() {
		var name string
		if scanErr := rows.Scan(&name); scanErr != nil {
			_ = rows.Close()
			return out, scanErr
		}
		names = append(names, name)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return out, err
	}
	if len(names) > 512 {
		return out, ErrProfileRetirementBound
	}
	var links []retirementLink
	for _, name := range names {
		rs, queryErr := tx.QueryContext(ctx, "PRAGMA foreign_key_list("+quoteRetirementName(name)+")")
		if queryErr != nil {
			return out, queryErr
		}
		for rs.Next() {
			var key, seq int
			var parent, from, update, deletion, match string
			var to sql.NullString
			if scanErr := rs.Scan(&key, &seq, &parent, &from, &to, &update, &deletion, &match); scanErr != nil {
				_ = rs.Close()
				return out, scanErr
			}
			// Unknown composite or implicit target keys refuse before export rather than omitting dependencies.
			links = append(links, retirementLink{name, parent, from, to.String, seq != 0 || !to.Valid || to.String == ""})
		}
		err = rs.Err()
		_ = rs.Close()
		if err != nil {
			return out, err
		}
	}
	predicates := map[string][]string{"agent_profiles": {`"id" = ?`}}
	// Retained history and legacy links do not all declare foreign keys.
	for _, table := range []string{"session_agents", "agent_projects", "agent_known_tools", "agent_known_skills", "agent_tools", "agent_dispatch_tool_allowlist", "agent_tools_legacy_backfill", "agent_procedures", "agent_knowledge_seed", "agent_log", "agent_schedules", "agent_reflexes", "messages", "agent_profile_revisions"} {
		predicates[table] = []string{`"agent_id" = ?`}
	}
	predicates["pending_reflexes"] = []string{`"target_agent_id" = ?`}
	predicates["durable_agent_instances"] = []string{`"profile_id" = ?`}
	// Each path contains one bound profile ID; cycle avoidance and an edge
	// budget bound both the schema walk and nested query size.
	paths := 0
	var walk func(string, string, map[string]bool) error
	walk = func(parent, predicate string, seen map[string]bool) error {
		for _, link := range links {
			if link.parent != parent || seen[link.child] {
				continue
			}
			if link.unsupported {
				return ErrProfileRetirementSchema
			}
			paths++
			if paths > 512 {
				return ErrProfileRetirementBound
			}
			childPredicate := quoteRetirementName(link.from) + " IN (SELECT " + quoteRetirementName(link.to) + " FROM " + quoteRetirementName(parent) + " WHERE " + predicate + ")"
			predicates[link.child] = append(predicates[link.child], childPredicate)
			next := map[string]bool{}
			for k, v := range seen {
				next[k] = v
			}
			next[link.child] = true
			if walkErr := walk(link.child, childPredicate, next); walkErr != nil {
				return walkErr
			}
		}
		return nil
	}
	if walkErr := walk("agent_profiles", `"id" = ?`, map[string]bool{"agent_profiles": true}); walkErr != nil {
		return out, walkErr
	}
	remaining, size := profileExportMaxRows, 0
	for _, table := range names {
		predicatesForTable := predicates[table]
		if len(predicatesForTable) == 0 {
			continue
		}
		args := make([]any, len(predicatesForTable))
		for i := range args {
			args[i] = id
		}
		// #nosec G202 -- schema-derived identifiers are quoted; the profile ID is bound for every predicate.
		rs, queryErr := tx.QueryContext(ctx, "SELECT * FROM "+quoteRetirementName(table)+" WHERE ("+strings.Join(predicatesForTable, ") OR (")+") LIMIT 10001", args...)
		if queryErr != nil {
			return out, queryErr
		}
		cols, columnsErr := rs.Columns()
		if columnsErr != nil {
			_ = rs.Close()
			return out, columnsErr
		}
		exported := ProfileExportTable{Columns: cols, Rows: []json.RawMessage{}}
		for rs.Next() {
			values := make([]any, len(cols))
			dest := make([]any, len(cols))
			for i := range values {
				dest[i] = &values[i]
			}
			if scanErr := rs.Scan(dest...); scanErr != nil {
				_ = rs.Close()
				return out, scanErr
			}
			encoded, encodeErr := json.Marshal(values)
			if encodeErr != nil {
				_ = rs.Close()
				return out, encodeErr
			}
			remaining--
			size += len(encoded)
			if remaining < 0 || size > ProfileExportMaxBytes {
				_ = rs.Close()
				return out, ErrProfileRetirementBound
			}
			exported.Rows = append(exported.Rows, encoded)
		}
		err = rs.Err()
		_ = rs.Close()
		if err != nil {
			return out, err
		}
		sort.Slice(exported.Rows, func(i, j int) bool { return bytes.Compare(exported.Rows[i], exported.Rows[j]) < 0 })
		out.Tables[table] = exported
	}
	_, err = out.Digest()
	return out, err
}

func deleteAgentTx(ctx context.Context, tx *sql.Tx, agent *AgentProfile) error {
	cleanups := []string{
		"DELETE FROM session_agents WHERE agent_id = ?",
		"DELETE FROM agent_projects WHERE agent_id = ?",
		// Per-agent capability/runtime children (migrations 068/070/074/085).
		// These declare FKs to agent_profiles(id); clean them explicitly so a
		// managed-agent delete leaves no orphaned reflexes, known tools/skills,
		// procedures, knowledge seeds, or schedules.
		"DELETE FROM agent_known_tools WHERE agent_id = ?",
		"DELETE FROM agent_known_skills WHERE agent_id = ?",
		// Phase 1 item 04 (migration 116): agent_tools/
		// agent_dispatch_tool_allowlist both already declare
		// ON DELETE CASCADE agent_profiles(id) FKs, so these two lines are
		// belt-and-suspenders, matching this list's existing style of
		// explicitly clearing agent_projects even though migration 113 gave
		// that a real cascade FK too.
		"DELETE FROM agent_tools WHERE agent_id = ?",
		"DELETE FROM agent_dispatch_tool_allowlist WHERE agent_id = ?",
		"DELETE FROM agent_tools_legacy_backfill WHERE agent_id = ?",
		"DELETE FROM agent_procedures WHERE agent_id = ?",
		"DELETE FROM agent_knowledge_seed WHERE agent_id = ?",
		"DELETE FROM agent_log WHERE agent_id = ?",
		"DELETE FROM agent_schedules WHERE agent_id = ?",
		"DELETE FROM agent_reflexes WHERE agent_id = ?",
		// pending_reflexes.target_agent_id references the profile (migration
		// 074, no cascade) — clear it or the final delete fails under
		// foreign_keys=ON. (pending_reflexes has no agent_id column.)
		"DELETE FROM pending_reflexes WHERE target_agent_id = ?",
		// durable_agent_instances.profile_id references the profile
		// (migration 080, no cascade; 081/082 only add columns). Removing the
		// instances cascades their instance_id children (sessions, events).
		// Deleting the managed profile is a permanent operator action, so its
		// durable instances go with it.
		"DELETE FROM durable_agent_instances WHERE profile_id = ?",
	}
	for _, q := range cleanups {
		if _, err := tx.ExecContext(ctx, q, agent.ID); err != nil {
			return fmt.Errorf("cleanup agent references (%s): %w", q, err)
		}
	}

	// Nullify agent_id on messages (preserve messages, just unlink the agent).
	if _, err := tx.ExecContext(ctx, "UPDATE messages SET agent_id = NULL WHERE agent_id = ?", agent.ID); err != nil {
		return fmt.Errorf("nullify messages for agent %s: %w", agent.Slug, err)
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM agent_profiles WHERE id = ?", agent.ID); err != nil {
		return fmt.Errorf("delete agent %s: %w", agent.Slug, err)
	}

	return nil
}
