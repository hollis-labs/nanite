package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Role represents the reusable persona/behavior template an Agent
// composition (agent_profiles) is built from -- see GLOSSARY.md's Role
// entry and architecture/01-agent-construction.md. A Role supplies the
// broadest layer of the role -> agent -> task cascade (closest wins);
// its default_* columns are seed hints a new composition can start from,
// not an authoritative live grant -- tools/skills/permissions still bind
// at the agent_profiles level. See internal/service/role_cascade.go for
// the cascade resolver that consumes this struct.
//
// roles is DB-authoritative from creation onward -- there is no
// file/YAML reingest path for this table (01-add-roles-table-and-cascade-
// resolution.md, verified again by 08-kill-file-reingest-on-boot-pattern.md).
type Role struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	// SystemPrompt is the role's persona/identity prompt -- the broadest
	// layer of the system_prompt cascade; an agent composition's own
	// system_prompt (if non-empty) overrides it, and a task/invocation
	// override (if any) overrides that.
	SystemPrompt string `json:"system_prompt"`
	// DefaultClass mirrors agent_profiles.class's four-value enum
	// (advisor/process/template/harness). Empty string means this role
	// supplies no class default. Validated at the Go layer (see
	// validateRoleFields), same approach as agent_profiles.class.
	DefaultClass string `json:"default_class"`
	// DefaultModel / DefaultProvider mirror agent_profiles.default_model/
	// default_provider -- the role-level default for model selection.
	DefaultModel    string `json:"default_model"`
	DefaultProvider string `json:"default_provider"`
	// DefaultTools is a JSON array of tool-name patterns a new composition
	// built from this role can start from -- mirrors agent_profiles.tools'
	// shape. Seed hint only, not an authoritative live grant.
	DefaultTools string `json:"default_tools"`
	// DefaultSkills is a JSON array of skill slugs, mirroring
	// agent_profiles.role_skills' shape. Same seed-hint-only caveat.
	DefaultSkills string `json:"default_skills"`
	// DefaultPermissions is a JSON object mirroring agent_profiles.
	// tool_permissions' shape. Same seed-hint-only caveat.
	DefaultPermissions string `json:"default_permissions"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`

	// PluginID tags this row as created/owned by a plugin's
	// registers.agent_profiles[] registration (Phase 5 item 03,
	// TASKS/phase-5/03-wire-registers-agent-profiles.md) -- mirrors
	// AgentProfile.PluginID's doc comment. Empty string means this role is
	// operator/GUI-created, or was reused (read-only, not claimed) by a
	// plugin registration that found an existing role at the same slug --
	// see agent_profiles.go's resolveOrCreatePluginRole. Added by
	// migration 122.
	PluginID string `json:"plugin_id"`
}

// validateRoleFields enforces the DefaultClass enum constraint at the Go
// layer, matching agent_profiles.class's own validation approach
// (validateAgentMultiAgentFields in agents.go) -- no CHECK constraint at
// the column level, same rationale (SQLite ALTER TABLE ADD COLUMN can't
// carry an idempotent CHECK after the fact; roles is a fresh CREATE TABLE
// so this isn't strictly forced by that constraint, but keeping validation
// symmetric with agent_profiles.class avoids two different enforcement
// styles for the same enum shape).
func validateRoleFields(r *Role) error {
	switch r.DefaultClass {
	case "", "advisor", "process", "template", "harness":
	default:
		return fmt.Errorf("default_class %q invalid: must be 'advisor', 'process', 'template', or 'harness'", r.DefaultClass)
	}
	return nil
}

// roleColumns is the canonical SELECT column list for roles.
const roleColumns = `id, slug, name, system_prompt, default_class, default_model, default_provider,
        default_tools, default_skills, default_permissions, created_at, updated_at,
        COALESCE(plugin_id,'')`

// scanRole scans a row into a Role using the canonical column order.
func scanRole(scanner interface{ Scan(...any) error }, r *Role) error {
	return scanner.Scan(
		&r.ID, &r.Slug, &r.Name, &r.SystemPrompt, &r.DefaultClass, &r.DefaultModel, &r.DefaultProvider,
		&r.DefaultTools, &r.DefaultSkills, &r.DefaultPermissions, &r.CreatedAt, &r.UpdatedAt,
		&r.PluginID,
	)
}

// ListRoles returns all roles ordered by name.
func (s *Store) ListRoles(ctx context.Context) ([]Role, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+roleColumns+` FROM roles ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	defer rows.Close()

	out := make([]Role, 0)
	for rows.Next() {
		var r Role
		if err := scanRole(rows, &r); err != nil {
			return nil, fmt.Errorf("scan role: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRole returns a role by ID, or nil if not found.
func (s *Store) GetRole(ctx context.Context, id string) (*Role, error) {
	var r Role
	row := s.DB.QueryRowContext(ctx, `SELECT `+roleColumns+` FROM roles WHERE id = ?`, id)
	if err := scanRole(row, &r); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("get role %s: %w", id, err)
	}
	return &r, nil
}

// GetRoleBySlug returns a role by slug, or nil if not found.
func (s *Store) GetRoleBySlug(ctx context.Context, slug string) (*Role, error) {
	var r Role
	row := s.DB.QueryRowContext(ctx, `SELECT `+roleColumns+` FROM roles WHERE slug = ?`, slug)
	if err := scanRole(row, &r); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("get role by slug %s: %w", slug, err)
	}
	return &r, nil
}

// CreateRole inserts a new role.
func (s *Store) CreateRole(ctx context.Context, r *Role) error {
	if r.ID == "" {
		r.ID = uuid.New().String()
	}
	if r.DefaultTools == "" {
		r.DefaultTools = "[]"
	}
	if r.DefaultSkills == "" {
		r.DefaultSkills = "[]"
	}
	if r.DefaultPermissions == "" {
		r.DefaultPermissions = "{}"
	}
	if err := validateRoleFields(r); err != nil {
		return fmt.Errorf("create role: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO roles (id, slug, name, system_prompt, default_class, default_model, default_provider,
		                    default_tools, default_skills, default_permissions, created_at, updated_at,
		                    plugin_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Slug, r.Name, r.SystemPrompt, r.DefaultClass, r.DefaultModel, r.DefaultProvider,
		r.DefaultTools, r.DefaultSkills, r.DefaultPermissions, now, now,
		nullIfEmpty(r.PluginID),
	)
	if err != nil {
		return fmt.Errorf("create role: %w", err)
	}
	r.CreatedAt = now
	r.UpdatedAt = now
	return nil
}

// UpdateRole updates a role's mutable fields.
func (s *Store) UpdateRole(ctx context.Context, r *Role) error {
	if r.DefaultTools == "" {
		r.DefaultTools = "[]"
	}
	if r.DefaultSkills == "" {
		r.DefaultSkills = "[]"
	}
	if r.DefaultPermissions == "" {
		r.DefaultPermissions = "{}"
	}
	if err := validateRoleFields(r); err != nil {
		return fmt.Errorf("update role: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.ExecContext(ctx,
		`UPDATE roles SET slug = ?, name = ?, system_prompt = ?, default_class = ?, default_model = ?,
		        default_provider = ?, default_tools = ?, default_skills = ?, default_permissions = ?,
		        updated_at = ?, plugin_id = ?
		 WHERE id = ?`,
		r.Slug, r.Name, r.SystemPrompt, r.DefaultClass, r.DefaultModel,
		r.DefaultProvider, r.DefaultTools, r.DefaultSkills, r.DefaultPermissions,
		now, nullIfEmpty(r.PluginID), r.ID,
	)
	if err != nil {
		return fmt.Errorf("update role: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("role %q not found", r.ID)
	}
	r.UpdatedAt = now
	return nil
}

// ListRolesByPluginID returns every roles row tagged with the given
// plugin_id (see Role.PluginID's doc comment / migration 122). Used by
// plugin.Host.UnloadPlugin's unload sweep.
func (s *Store) ListRolesByPluginID(ctx context.Context, pluginID string) ([]Role, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+roleColumns+` FROM roles WHERE plugin_id = ? ORDER BY slug`, pluginID)
	if err != nil {
		return nil, fmt.Errorf("list roles by plugin_id: %w", err)
	}
	defer rows.Close()

	out := make([]Role, 0)
	for rows.Next() {
		var r Role
		if err := scanRole(rows, &r); err != nil {
			return nil, fmt.Errorf("scan role: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteRole removes a role by ID.
func (s *Store) DeleteRole(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM roles WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete role %s: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("role %q not found", id)
	}
	return nil
}
