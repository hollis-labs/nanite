package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Skill represents a skill record that binds a name/description to MCP tools.
type Skill struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	Description  string `json:"description"`
	Category     string `json:"category"`
	ToolBindings string `json:"tool_bindings"` // JSON array of tool names
	InputSchema  string `json:"input_schema"`  // JSON schema
	IsBuiltin    bool   `json:"is_builtin"`
	Settings     string `json:"settings"`
	Icon         string `json:"icon"`
	Prompt       string `json:"prompt,omitempty"` // markdown body; set for file-based skills, empty for DB-only
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// AgentSkill represents an assignment of a skill to an agent.
type AgentSkill struct {
	AgentID string `json:"agent_id"`
	SkillID string `json:"skill_id"`
	Config  string `json:"config"`
}

// ListSkills returns all skills ordered by name.
func (s *Store) ListSkills() ([]Skill, error) {
	rows, err := s.DB.Query(
		`SELECT id, name, slug, description, category, tool_bindings, input_schema, is_builtin, settings, COALESCE(icon,''), created_at, updated_at
		 FROM skills ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	defer rows.Close()

	out := make([]Skill, 0)
	for rows.Next() {
		var sk Skill
		if err := rows.Scan(&sk.ID, &sk.Name, &sk.Slug, &sk.Description, &sk.Category,
			&sk.ToolBindings, &sk.InputSchema, &sk.IsBuiltin, &sk.Settings, &sk.Icon,
			&sk.CreatedAt, &sk.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan skill: %w", err)
		}
		out = append(out, sk)
	}
	return out, rows.Err()
}

// GetSkill returns a skill by ID.
func (s *Store) GetSkill(id string) (*Skill, error) {
	var sk Skill
	err := s.DB.QueryRow(
		`SELECT id, name, slug, description, category, tool_bindings, input_schema, is_builtin, settings, COALESCE(icon,''), created_at, updated_at
		 FROM skills WHERE id = ?`, id,
	).Scan(&sk.ID, &sk.Name, &sk.Slug, &sk.Description, &sk.Category,
		&sk.ToolBindings, &sk.InputSchema, &sk.IsBuiltin, &sk.Settings, &sk.Icon,
		&sk.CreatedAt, &sk.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get skill %s: %w", id, err)
	}
	return &sk, nil
}

// GetSkillBySlug returns a skill by slug.
func (s *Store) GetSkillBySlug(slug string) (*Skill, error) {
	var sk Skill
	err := s.DB.QueryRow(
		`SELECT id, name, slug, description, category, tool_bindings, input_schema, is_builtin, settings, COALESCE(icon,''), created_at, updated_at
		 FROM skills WHERE slug = ?`, slug,
	).Scan(&sk.ID, &sk.Name, &sk.Slug, &sk.Description, &sk.Category,
		&sk.ToolBindings, &sk.InputSchema, &sk.IsBuiltin, &sk.Settings, &sk.Icon,
		&sk.CreatedAt, &sk.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get skill by slug %s: %w", slug, err)
	}
	return &sk, nil
}

// CreateSkill inserts a new skill.
func (s *Store) CreateSkill(sk *Skill) error {
	if sk.ID == "" {
		sk.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if sk.ToolBindings == "" {
		sk.ToolBindings = "[]"
	}
	if sk.InputSchema == "" {
		sk.InputSchema = "{}"
	}
	if sk.Settings == "" {
		sk.Settings = "{}"
	}

	_, err := s.DB.Exec(
		`INSERT INTO skills (id, name, slug, description, category, tool_bindings, input_schema, is_builtin, settings, icon, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sk.ID, sk.Name, sk.Slug, sk.Description, sk.Category,
		sk.ToolBindings, sk.InputSchema, sk.IsBuiltin, sk.Settings, nullIfEmpty(sk.Icon),
		now, now,
	)
	if err != nil {
		return fmt.Errorf("create skill: %w", err)
	}
	sk.CreatedAt = now
	sk.UpdatedAt = now
	return nil
}

// UpdateSkill updates a skill's mutable fields.
func (s *Store) UpdateSkill(sk *Skill) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(
		`UPDATE skills SET name = ?, slug = ?, description = ?, category = ?,
		        tool_bindings = ?, input_schema = ?, settings = ?, icon = ?, updated_at = ?
		 WHERE id = ?`,
		sk.Name, sk.Slug, sk.Description, sk.Category,
		sk.ToolBindings, sk.InputSchema, sk.Settings, nullIfEmpty(sk.Icon),
		now, sk.ID,
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

// DeleteSkill removes a skill by ID (only non-builtin).
func (s *Store) DeleteSkill(id string) error {
	res, err := s.DB.Exec(
		`DELETE FROM skills WHERE id = ? AND is_builtin = 0`, id,
	)
	if err != nil {
		return fmt.Errorf("delete skill %s: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("skill %q not found or is built-in", id)
	}
	return nil
}

// ListAgentSkills returns all skills assigned to an agent.
func (s *Store) ListAgentSkills(agentID string) ([]Skill, error) {
	rows, err := s.DB.Query(
		`SELECT sk.id, sk.name, sk.slug, sk.description, sk.category, sk.tool_bindings,
		        sk.input_schema, sk.is_builtin, sk.settings, COALESCE(sk.icon,''),
		        sk.created_at, sk.updated_at
		 FROM skills sk
		 JOIN agent_skills ags ON sk.id = ags.skill_id
		 WHERE ags.agent_id = ?
		 ORDER BY sk.name`, agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list agent skills: %w", err)
	}
	defer rows.Close()

	out := make([]Skill, 0)
	for rows.Next() {
		var sk Skill
		if err := rows.Scan(&sk.ID, &sk.Name, &sk.Slug, &sk.Description, &sk.Category,
			&sk.ToolBindings, &sk.InputSchema, &sk.IsBuiltin, &sk.Settings, &sk.Icon,
			&sk.CreatedAt, &sk.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan agent skill: %w", err)
		}
		out = append(out, sk)
	}
	return out, rows.Err()
}

// AssignSkillToAgent links a skill to an agent.
func (s *Store) AssignSkillToAgent(agentID, skillID, config string) error {
	if config == "" {
		config = "{}"
	}
	_, err := s.DB.Exec(
		`INSERT OR IGNORE INTO agent_skills (agent_id, skill_id, config)
		 VALUES (?, ?, ?)`,
		agentID, skillID, config,
	)
	if err != nil {
		return fmt.Errorf("assign skill to agent: %w", err)
	}
	return nil
}

// RemoveSkillFromAgent removes a skill assignment from an agent.
func (s *Store) RemoveSkillFromAgent(agentID, skillID string) error {
	res, err := s.DB.Exec(
		`DELETE FROM agent_skills WHERE agent_id = ? AND skill_id = ?`,
		agentID, skillID,
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

// BuiltinSkills defines the default skills matching existing tools.
var BuiltinSkills = []Skill{
	{
		Name:         "Dev Read",
		Slug:         "dev-read",
		Description:  "Read file contents with optional line range",
		Category:     "dev",
		ToolBindings: `["dev_read"]`,
	},
	{
		Name:         "Dev Write",
		Slug:         "dev-write",
		Description:  "Write content to a file",
		Category:     "dev",
		ToolBindings: `["dev_write"]`,
	},
	{
		Name:         "Dev Grep",
		Slug:         "dev-grep",
		Description:  "Search files matching a regex pattern",
		Category:     "dev",
		ToolBindings: `["dev_grep"]`,
	},
	{
		Name:         "Dev Bash",
		Slug:         "dev-bash",
		Description:  "Execute shell commands",
		Category:     "dev",
		ToolBindings: `["dev_bash"]`,
	},
	{
		Name:         "Dev Glob",
		Slug:         "dev-glob",
		Description:  "Find files matching a glob pattern",
		Category:     "dev",
		ToolBindings: `["dev_glob"]`,
	},
	{
		Name:         "Dev Edit",
		Slug:         "dev-edit",
		Description:  "Edit a file by replacing a string",
		Category:     "dev",
		ToolBindings: `["dev_edit"]`,
	},
	{
		Name:         "Math Evaluate",
		Slug:         "math-evaluate",
		Description:  "Evaluate arithmetic expressions",
		Category:     "general",
		ToolBindings: `["math_eval"]`,
	},
	{
		Name:         "Encoding Convert",
		Slug:         "encoding-convert",
		Description:  "Base64, URL encoding/decoding, and hashing",
		Category:     "general",
		ToolBindings: `["base64_encode","base64_decode","url_encode","url_decode","hash"]`,
	},
}

// SeedBuiltinSkills inserts built-in skills if they don't exist.
func (s *Store) SeedBuiltinSkills() error {
	now := time.Now().UTC().Format(time.RFC3339)
	for _, sk := range BuiltinSkills {
		_, err := s.DB.Exec(
			`INSERT OR IGNORE INTO skills (id, name, slug, description, category, tool_bindings, input_schema, is_builtin, settings, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, '{}', 1, '{}', ?, ?)`,
			uuid.New().String(), sk.Name, sk.Slug, sk.Description, sk.Category, sk.ToolBindings, now, now,
		)
		if err != nil {
			return fmt.Errorf("seed skill %s: %w", sk.Slug, err)
		}
	}
	return nil
}
