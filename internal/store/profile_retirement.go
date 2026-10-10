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

	mesh "github.com/hollis-labs/substrate/mesh"
)

var (
	ErrProfileRetirementProtected = errors.New("profile is not editable")
	ErrProfileRetirementSchema    = errors.New("profile export dependency schema is unsupported")
	ErrProfileRetirementConflict  = errors.New("profile state changed after export")
	ErrProfileRetirementBound     = errors.New("profile export exceeds its bound")
	ErrProfileRetirementKeep      = errors.New("protected retirement requires the exact approved historical General Chat")
	ErrProfileRetirementActive    = errors.New("profile has active or uncertain runtime activity")
)

const ProfileExportMaxBytes = 16 << 20
const profileExportMaxRows = 10000
const profileExportSchemaVersion = 2

// ProtectedProfileExportSchemaVersion includes referenced parent content. Editable format 2 is
// deliberately unchanged; a protected format-2 archive needs a fresh export.
const ProtectedProfileExportSchemaVersion = 3

// ProfileRetirementExport contains a consistent, private export of the profile
// and its affected relational records. It is data, never an import or grant.
type ProfileRetirementExport struct {
	SchemaVersion int                           `json:"schema_version"`
	ProfileID     string                        `json:"profile_id"`
	Slug          string                        `json:"slug"`
	Revision      string                        `json:"revision"`
	Tables        map[string]ProfileExportTable `json:"tables"`
}

// ProfileExportTable rows contain typed SQLite cells. TEXT and BLOB values are base64-encoded
// bytes so even invalid UTF-8 text survives without JSON replacement.
type ProfileExportTable struct {
	Columns []string          `json:"columns"`
	Rows    []json.RawMessage `json:"rows"`
}

// Keep SQLite storage classes distinct: plain JSON conflates BLOB with TEXT
// and INTEGER with REAL. That would let changed state pass the deletion guard.
type profileExportCell struct {
	Type  string `json:"type"`
	Value any    `json:"value"`
}

func encodeProfileExportRow(values []any) (json.RawMessage, error) {
	cells := make([]profileExportCell, len(values))
	for i, value := range values {
		switch v := value.(type) {
		case nil:
			cells[i] = profileExportCell{Type: "null"}
		case int64:
			cells[i] = profileExportCell{Type: "integer", Value: v}
		case float64:
			cells[i] = profileExportCell{Type: "real", Value: v}
		case string:
			cells[i] = profileExportCell{Type: "text", Value: []byte(v)}
		case []byte:
			// A zero-length BLOB is distinct from NULL, including a nil slice
			// returned by the driver for an empty BLOB.
			data := make([]byte, len(v))
			copy(data, v)
			cells[i] = profileExportCell{Type: "blob", Value: data}
		default:
			return nil, ErrProfileRetirementSchema
		}
	}
	return json.Marshal(cells)
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
	return s.exportProfileRetirement(ctx, id, false)
}

// ExportProtectedProfileRetirement snapshots an explicitly selected profile,
// including Nanite/plugin-owned classes, before an audited retirement request.
func (s *Store) ExportProtectedProfileRetirement(ctx context.Context, id string) (ProfileRetirementExport, error) {
	return s.exportProfileRetirement(ctx, id, true)
}

func (s *Store) exportProfileRetirement(ctx context.Context, id string, includeProtected bool) (ProfileRetirementExport, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ProfileRetirementExport{}, err
	}
	defer rollbackUnlessCommitted(tx)
	snapshot, err := profileRetirementSnapshot(ctx, tx, id, includeProtected)
	if err != nil {
		return ProfileRetirementExport{}, err
	}
	return snapshot, tx.Commit()
}

// RetireExportedProfile compares the complete export and applies the existing
// cleanup in the same write transaction. Profile revisions alone do not cover
// mutable grants, schedules and other children.
func (s *Store) RetireExportedProfile(ctx context.Context, id, digest string) error {
	return s.RetireExportedProfileWithAudit(ctx, id, digest, RetireAgentProfileAudit{})
}

// RetireExportedProfileWithAudit compares the exported state, records a
// durable tombstone, and deletes the profile in one write transaction.
func (s *Store) RetireExportedProfileWithAudit(ctx context.Context, id, digest string, audit RetireAgentProfileAudit) error {
	if id == "" || digest == "" {
		return ErrProfileRetirementConflict
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	includeProtected := audit.ExportID != ""
	if includeProtected {
		if keepErr := checkProtectedRetirementKeep(ctx, tx, id, audit.Keep); keepErr != nil {
			return keepErr
		}
	}
	snapshot, err := profileRetirementSnapshot(ctx, tx, id, includeProtected)
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
	profile, err := getHistoricalAgentProfile(ctx, tx, id)
	if err != nil {
		return err
	}
	if audit.ExportID != "" {
		if err := checkProtectedRetirementActivity(ctx, tx, profile); err != nil {
			return err
		}
		audit.Digest = digest
		if err := insertRetiredAgentProfileTx(ctx, tx, profile, audit); err != nil {
			return err
		}
		// These historical children are included by the snapshot FK walk but
		// have restrictive FKs. Remove only this profile's exported terminal
		// links; the referenced teams/workflows and fresh actor graph survive.
		for _, query := range []string{
			`DELETE FROM team_run_members WHERE agent_id=?`,
			`DELETE FROM team_run_member_intents WHERE agent_id=?`,
		} {
			if _, err := tx.ExecContext(ctx, query, id); err != nil {
				return err
			}
		}
	}
	if err := deleteAgentTx(ctx, tx, profile); err != nil {
		return err
	}
	if includeProtected {
		// Recheck after cleanup as well: database triggers must not change the
		// retained row while this transaction records/deletes another profile.
		if keepErr := checkProtectedRetirementKeep(ctx, tx, id, audit.Keep); keepErr != nil {
			return keepErr
		}
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

func profileRetirementSnapshot(ctx context.Context, tx *sql.Tx, id string, includeProtected bool) (ProfileRetirementExport, error) {
	profile, err := getHistoricalAgentProfile(ctx, tx, id)
	if err != nil {
		return ProfileRetirementExport{}, err
	}
	if !includeProtected && !retirementEditable(profile) {
		return ProfileRetirementExport{}, ErrProfileRetirementProtected
	}
	out := ProfileRetirementExport{SchemaVersion: profileExportSchemaVersion, ProfileID: profile.ID, Slug: profile.Slug, Revision: profile.Revision, Tables: map[string]ProfileExportTable{}}
	if includeProtected {
		out.SchemaVersion = ProtectedProfileExportSchemaVersion
	}
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
	if includeProtected {
		// Referenced parents survive deletion, but are part of the exported
		// behavior/provenance and therefore the same-transaction digest CAS.
		for table, column := range map[string]string{"roles": "role_id", "consumers": "consumer_id", "models": "model_id"} {
			predicates[table] = []string{`"id" IN (SELECT ` + quoteRetirementName(column) + ` FROM "agent_profiles" WHERE "id" = ?)`}
		}
	}
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
		rs, queryErr := tx.QueryContext(ctx, "SELECT * FROM "+quoteRetirementName(table)+" LIMIT 0")
		if queryErr != nil {
			return out, queryErr
		}
		cols, columnsErr := rs.Columns()
		if columnsErr != nil {
			_ = rs.Close()
			return out, columnsErr
		}
		if closeErr := rs.Close(); closeErr != nil {
			return out, closeErr
		}
		// Expression columns expose raw SQLite storage classes. Direct columns
		// with DATETIME/BOOLEAN declarations can be converted by the driver to
		// time.Time/bool, losing the original text or integer representation.
		expressions := make([]string, len(cols))
		for i, col := range cols {
			expressions[i] = "CASE WHEN 1 THEN " + quoteRetirementName(col) + " END"
		}
		// #nosec G202 -- schema-derived identifiers are quoted; profile IDs are bound.
		rs, queryErr = tx.QueryContext(ctx, "SELECT "+strings.Join(expressions, ",")+" FROM "+quoteRetirementName(table)+" WHERE ("+strings.Join(predicatesForTable, ") OR (")+") LIMIT 10001", args...)
		if queryErr != nil {
			return out, queryErr
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
			encoded, encodeErr := encodeProfileExportRow(values)
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

func checkProtectedRetirementKeep(ctx context.Context, tx *sql.Tx, target string, keep ProtectedRetirementKeep) error {
	if keep.ID == "" || keep.Revision == "" || keep.ID == target || keep.DefinitionRef.ID == "" || keep.DefinitionRef.Revision == "" || keep.DefinitionRef.Digest == "" || keep.SystemPrompt == "" {
		return ErrProfileRetirementKeep
	}
	p, err := getHistoricalAgentProfile(ctx, tx, keep.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrProfileRetirementKeep
	}
	if err != nil {
		return err
	}
	var settings struct {
		Pin struct {
			ID       string `json:"definition_id"`
			Revision string `json:"revision"`
			Digest   string `json:"semantic_digest"`
		} `json:"provisioned_definition_ref"`
	}
	if err := json.Unmarshal([]byte(p.Settings), &settings); err != nil {
		return ErrProfileRetirementKeep
	}
	pin := mesh.DefinitionRef{ID: settings.Pin.ID, Revision: settings.Pin.Revision, Digest: settings.Pin.Digest}
	if p.Revision != keep.Revision || pin != keep.DefinitionRef || p.SystemPrompt != keep.SystemPrompt {
		return ErrProfileRetirementKeep
	}
	return nil
}

// Historical markers can refuse retirement; they never enroll an actor or
// authorize execution. Unknown/nonterminal states are not evidence of a stop.
// The live operator must quiesce the old runtime before deploying the fresh cut.
func checkProtectedRetirementActivity(ctx context.Context, tx *sql.Tx, p *AgentProfile) error {
	const sessions = `SELECT session_id FROM session_agents WHERE agent_id=?1
		UNION SELECT current_session_id FROM durable_agent_instances WHERE profile_id=?1 AND current_session_id<>''
		UNION SELECT r.session_id FROM durable_agent_instance_sessions r JOIN durable_agent_instances d ON d.id=r.instance_id WHERE d.profile_id=?1
		UNION SELECT session_id FROM team_run_members WHERE agent_id=?1`
	queries := []string{
		`SELECT EXISTS(SELECT 1 FROM durable_agent_instances WHERE profile_id=?1 AND status NOT IN ('sleeping','stopped','failed','archived'))`,
		`SELECT EXISTS(SELECT 1 FROM agent_runtime WHERE (agent_profile=?1 OR agent_profile=?2 OR id IN (` + sessions + `) OR parent_session_id IN (` + sessions + `)) AND state NOT IN ('done','failed'))`,
		`SELECT EXISTS(SELECT 1 FROM subagent_runs WHERE (parent_agent_id=?1 OR role=?2 OR parent_session_id IN (` + sessions + `) OR child_session_id IN (` + sessions + `)) AND status NOT IN ('completed','failed','canceled','rejected','over_budget'))`,
		`SELECT EXISTS(SELECT 1 FROM team_run_members WHERE agent_id=?1 AND status NOT IN ('stopped','failed','replaced'))`,
		`SELECT EXISTS(SELECT 1 FROM workflow_runs w JOIN team_run_members m ON m.workflow_run_id=w.id WHERE m.agent_id=?1 AND (w.status NOT IN ('completed','failed','canceled') OR (w.runtime_status IS NOT NULL AND w.runtime_status NOT IN ('completed','failed','canceled'))))`,
		`SELECT EXISTS(SELECT 1 FROM loop_runs l JOIN loop_run_iterations i ON i.loop_run_id=l.id JOIN team_run_members m ON m.workflow_run_id=i.workflow_run_id WHERE m.agent_id=?1 AND l.status NOT IN ('completed','failed','canceled'))`,
		`SELECT EXISTS(SELECT 1 FROM team_run_member_intents i LEFT JOIN team_run_launches l ON l.idempotency_key=i.idempotency_key LEFT JOIN workflow_runs w ON w.id=l.workflow_run_id WHERE i.agent_id=?1 AND (w.id IS NULL OR w.status NOT IN ('completed','failed','canceled')))`,
	}
	for _, query := range queries {
		var active bool
		// Numbered parameters reuse the exact historical ID in nested predicates.
		args := []any{p.ID}
		if strings.Contains(query, "?2") {
			args = append(args, p.Slug)
		}
		if err := tx.QueryRowContext(ctx, query, args...).Scan(&active); err != nil {
			return fmt.Errorf("check historical retirement activity: %w", err)
		}
		if active {
			return ErrProfileRetirementActive
		}
	}
	return nil
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
