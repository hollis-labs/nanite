package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Skill is an index-only row over an authored, Agent-Skills-spec-compatible
// package vendored into the content-addressed skill store (internal/
// skillvendor). TASKS/skills/02 (docs/engineering/architecture/20-skills.md's
// "The model: DB is an index, a vendored store is content"): this row never
// holds SKILL.md body text, script contents, or asset bytes — it holds
// vendored-store addressing, version, source tier, enablement, and (once
// composition ships, task 07) declared dependencies. Materialization always
// reads the vendored copy live via internal/skillvendor.Store, keyed by
// ContentHash, never a value baked into this struct at install time.
//
// TASKS/skills/01 already cut this table's two prior writers (mcp.Manager.
// AutoDiscover's per-tool rows, and the file-based builtin-skill seed) — the
// table was confirmed empty of any content this redesign carries forward.
// TASKS/skills/02 (this task) redesigns the row shape itself: Prompt (the
// markdown body — content duplication, now vendored instead),
// ToolBindings (populated almost entirely by the now-cut AutoDiscover),
// IsBuiltin (the builtin/non-builtin split is folded into SourceTier), and
// ModeIDs (E2-era mode binding, tied to the now-cut steering-modes system —
// confirmed via grep to have exactly one remaining production reader,
// internal/service/skill.go's file-def backfill block, itself removed in
// this same task) are all dropped, not merely deprecated.
type Skill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Icon        string `json:"icon"`
	InputSchema string `json:"input_schema"` // JSON schema for declared parameters

	// SourceTier replaces the old bare Source string. Taxonomy is owned by
	// task 04's package parser; this task treats it as a free-form string
	// ("user" by default) rather than pre-committing to an enum ahead of
	// that parser landing.
	SourceTier string `json:"source_tier"`

	// ContentHash is the addressing key into the vendored store
	// (internal/skillvendor.Store — TASKS/skills/03), e.g.
	// "skl-vendor-<hash[:16]>". Empty until an install/sync (task 04/05)
	// actually vendors a package for this row.
	ContentHash string `json:"content_hash"`

	// Version is bumped on each re-install/re-sync that changes ContentHash.
	Version int `json:"version"`

	// Enabled replaces the old is_builtin/removed-in-Settings hack this
	// table used to approximate enablement with.
	Enabled bool `json:"enabled"`

	// DeclaredDependencies is a JSON array of skill slugs — the install-time
	// dependency graph task 07's cycle detection walks. "[]" when the
	// package declares no composition.
	DeclaredDependencies string `json:"declared_dependencies"`

	InstalledAt string `json:"installed_at"`
	UpdatedAt   string `json:"updated_at"`
}

// skillColumns is the canonical SELECT column list for skills.
const skillColumns = `id, name, slug, description, category, COALESCE(icon,''),
        input_schema, COALESCE(source_tier,'user'), COALESCE(content_hash,''),
        version, enabled, declared_dependencies, installed_at, updated_at`

// scanSkill scans a row into a Skill using the canonical column order.
func scanSkill(scanner interface{ Scan(...any) error }, sk *Skill) error {
	return scanner.Scan(
		&sk.ID, &sk.Name, &sk.Slug, &sk.Description, &sk.Category, &sk.Icon,
		&sk.InputSchema, &sk.SourceTier, &sk.ContentHash,
		&sk.Version, &sk.Enabled, &sk.DeclaredDependencies, &sk.InstalledAt, &sk.UpdatedAt,
	)
}

// ListSkills returns all skills ordered by name.
func (s *Store) ListSkills() ([]Skill, error) {
	rows, err := s.DB.Query(`SELECT ` + skillColumns + ` FROM skills ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	defer rows.Close()

	out := make([]Skill, 0)
	for rows.Next() {
		var sk Skill
		if err := scanSkill(rows, &sk); err != nil {
			return nil, fmt.Errorf("scan skill: %w", err)
		}
		out = append(out, sk)
	}
	return out, rows.Err()
}

// GetSkill returns a skill by ID.
func (s *Store) GetSkill(id string) (*Skill, error) {
	var sk Skill
	row := s.DB.QueryRow(`SELECT `+skillColumns+` FROM skills WHERE id = ?`, id)
	if err := scanSkill(row, &sk); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("get skill %s: %w", id, err)
	}
	return &sk, nil
}

// GetSkillBySlug returns a skill by slug.
func (s *Store) GetSkillBySlug(slug string) (*Skill, error) {
	var sk Skill
	row := s.DB.QueryRow(`SELECT `+skillColumns+` FROM skills WHERE slug = ?`, slug)
	if err := scanSkill(row, &sk); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("get skill by slug %s: %w", slug, err)
	}
	return &sk, nil
}

// CreateSkill inserts a new skill index row.
func (s *Store) CreateSkill(sk *Skill) error {
	if sk.ID == "" {
		sk.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if sk.InputSchema == "" {
		sk.InputSchema = "{}"
	}
	if sk.SourceTier == "" {
		sk.SourceTier = "user"
	}
	if sk.Version == 0 {
		sk.Version = 1
	}
	if sk.DeclaredDependencies == "" {
		sk.DeclaredDependencies = "[]"
	}

	_, err := s.DB.Exec(
		`INSERT INTO skills (id, name, slug, description, category, icon, input_schema,
		                     source_tier, content_hash, version, enabled,
		                     declared_dependencies, installed_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sk.ID, sk.Name, sk.Slug, sk.Description, sk.Category, nullIfEmpty(sk.Icon), sk.InputSchema,
		sk.SourceTier, nullIfEmpty(sk.ContentHash), sk.Version, sk.Enabled,
		sk.DeclaredDependencies, now, now,
	)
	if err != nil {
		return fmt.Errorf("create skill: %w", err)
	}
	sk.InstalledAt = now
	sk.UpdatedAt = now
	return nil
}

// UpdateSkill updates a skill's mutable fields.
func (s *Store) UpdateSkill(sk *Skill) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if sk.DeclaredDependencies == "" {
		sk.DeclaredDependencies = "[]"
	}
	res, err := s.DB.Exec(
		`UPDATE skills SET name = ?, slug = ?, description = ?, category = ?, icon = ?,
		        input_schema = ?, source_tier = ?, content_hash = ?, version = ?, enabled = ?,
		        declared_dependencies = ?, updated_at = ?
		 WHERE id = ?`,
		sk.Name, sk.Slug, sk.Description, sk.Category, nullIfEmpty(sk.Icon),
		sk.InputSchema, sk.SourceTier, nullIfEmpty(sk.ContentHash), sk.Version, sk.Enabled,
		sk.DeclaredDependencies, now, sk.ID,
	)
	if err != nil {
		return fmt.Errorf("update skill: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("skill %q not found", sk.ID)
	}
	sk.UpdatedAt = now
	return nil
}

// DeleteSkill removes a skill by ID.
//
// TASKS/skills/02: the old "AND is_builtin = 0" guard is dropped along with
// the IsBuiltin field itself — the builtin/non-builtin split this table used
// to carry no longer exists (task 01 deleted every builtin row and its
// seeder). Any remaining "can this skill be deleted" policy (e.g. a
// plugin-owned SourceTier) belongs to task 12's real uninstall semantics,
// not this bare index-row delete.
func (s *Store) DeleteSkill(id string) error {
	res, err := s.DB.Exec(`DELETE FROM skills WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete skill %s: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("skill %q not found", id)
	}
	return nil
}

// ListAgentSkills returns all skills granted to an agent.
//
// TASKS/skills/02: the old, dedicated agent<->skill join table (created by
// 001_schema.sql, FK-hardened by 113_agent_skills_agent_projects_fk.sql) is
// dropped in full by migration 137 — confirmed zero rows workspace-wide,
// but real live code depended on the mechanism (this function's own
// caller, internal/chat's buildSkillListForSession, feeds the API-direct
// chat prompt's skill catalog block; the Agent Builder Wizard's capability-
// assignment step; internal/plugin/agent_profiles.go's declarative
// skill-grant provisioning). Per docs/engineering/architecture/
// 13-memory-and-knowledge-tools.md §4a and this task's own instruction,
// agent_known_skills (the per-agent grant/attachment table, extended by
// this same task with grant-state columns) is now the sole per-agent skill
// attachment mechanism — this function joins through it by slug rather than
// the dropped table's skill_id FK. A known-skill row whose skill_name
// doesn't match any current skill's slug is silently excluded, matching the
// old table's FK-cascade behavior (an assignment to a since-deleted skill no
// longer resolves).
func (s *Store) ListAgentSkills(agentID string) ([]Skill, error) {
	rows, err := s.DB.Query(
		`SELECT `+skillColumns+`
		 FROM skills sk
		 JOIN agent_known_skills aks ON aks.skill_name = sk.slug
		 WHERE aks.agent_id = ?
		 ORDER BY sk.name`, agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list agent skills: %w", err)
	}
	defer rows.Close()

	out := make([]Skill, 0)
	for rows.Next() {
		var sk Skill
		if err := scanSkill(rows, &sk); err != nil {
			return nil, fmt.Errorf("scan agent skill: %w", err)
		}
		out = append(out, sk)
	}
	return out, rows.Err()
}

// AssignSkillToAgent grants a skill to an agent.
//
// TASKS/skills/02: backed by agent_known_skills, keyed by the skill's slug
// (matching internal/plugin/agent_profiles.go's existing slug-based grant
// convention for plugin-declared agents), not the dropped join table's
// skill_id FK (see ListAgentSkills' doc comment for that table's history).
// config is accepted for backward call-site/REST-contract compatibility
// with the pre-redesign join table's own config column, but is not
// persisted — agent_known_skills has no free-form config column;
// capabilities_granted (task 09's typed grant-state column) is the real
// successor once a grant needs to carry execution-capability data.
// agent_known_skills.agent_id still carries the same REFERENCES
// agent_profiles(id) the dropped table used to enforce, so an assignment
// against a nonexistent agent_id is still rejected at the DB level.
func (s *Store) AssignSkillToAgent(agentID, skillID, _ string) error {
	sk, err := s.GetSkill(skillID)
	if err != nil {
		return fmt.Errorf("assign skill to agent: %w", err)
	}
	if sk == nil {
		return fmt.Errorf("assign skill to agent: skill %q not found", skillID)
	}
	_, err = s.DB.Exec(
		`INSERT INTO agent_known_skills (agent_id, skill_name) VALUES (?, ?)
		 ON CONFLICT(agent_id, skill_name) DO NOTHING`,
		agentID, sk.Slug,
	)
	if err != nil {
		return fmt.Errorf("assign skill to agent: %w", err)
	}
	return nil
}

// RemoveSkillFromAgent removes a skill assignment from an agent.
//
// TASKS/skills/02: backed by agent_known_skills — see AssignSkillToAgent.
//
// TASKS/skills/02's fix-required section (2026-08-21 review): this "unassign"
// endpoint (DELETE /api/agents/{id}/skills/{id}) shares agent_known_skills'
// row space with the completely separate known-skills grant/telemetry
// surface (POST/PUT/DELETE /api/agents/{id}/known-skills,
// internal/api/agent_capabilities.go). "Assignment" isn't a real column
// here — just row existence — so an unconditional DELETE would destroy any
// pinned/activation_count/last_used_at/ttl_seconds/reason/
// approved_content_hash/granted_at/granted_by/capabilities_granted data that
// other surface separately set for the same agent+skill. Only a bare row
// (AgentKnownSkill.IsBareAssignment — no known-skill data of its own) is
// physically deleted; a row carrying real known-skill data is left intact
// and this call still reports success (nil error) — from the assignment
// endpoint's own perspective the skill is unassigned, and the frontend
// caller (AgentProfileManager.tsx's removeSkillMutation) surfaces no error
// UI for this call regardless, so a silent no-op here is strictly safer
// than either destroying grant data or leaving the caller with an
// unactionable failure.
func (s *Store) RemoveSkillFromAgent(agentID, skillID string) error {
	sk, err := s.GetSkill(skillID)
	if err != nil {
		return fmt.Errorf("remove skill from agent: %w", err)
	}
	if sk == nil {
		return fmt.Errorf("remove skill from agent: skill %q not found", skillID)
	}
	existing, err := s.GetAgentKnownSkill(context.Background(), agentID, sk.Slug)
	if err != nil {
		if errors.Is(err, ErrAgentKnownSkillNotFound) {
			return fmt.Errorf("skill assignment not found")
		}
		return fmt.Errorf("remove skill from agent: %w", err)
	}
	if !existing.IsBareAssignment() {
		return nil
	}
	res, err := s.DB.Exec(
		`DELETE FROM agent_known_skills WHERE agent_id = ? AND skill_name = ?`,
		agentID, sk.Slug,
	)
	if err != nil {
		return fmt.Errorf("remove skill from agent: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("skill assignment not found")
	}
	return nil
}
