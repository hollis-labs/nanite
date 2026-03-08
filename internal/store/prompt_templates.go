package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// PromptTemplate represents a composable system prompt fragment.
type PromptTemplate struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Scope     string `json:"scope"` // system, mode, skill, context
	Template  string `json:"template"`
	Variables string `json:"variables"` // JSON array of variable names
	Priority  int    `json:"priority"`
	IsBuiltin bool   `json:"is_builtin"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// ListPromptTemplates returns all prompt templates ordered by priority.
func (s *Store) ListPromptTemplates() ([]PromptTemplate, error) {
	rows, err := s.DB.Query(
		`SELECT id, name, slug, scope, template, variables, priority, is_builtin, created_at, updated_at
		 FROM prompt_templates ORDER BY priority, name`,
	)
	if err != nil {
		return nil, fmt.Errorf("list prompt templates: %w", err)
	}
	defer rows.Close()

	out := make([]PromptTemplate, 0)
	for rows.Next() {
		var pt PromptTemplate
		if err := rows.Scan(&pt.ID, &pt.Name, &pt.Slug, &pt.Scope, &pt.Template,
			&pt.Variables, &pt.Priority, &pt.IsBuiltin, &pt.CreatedAt, &pt.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan prompt template: %w", err)
		}
		out = append(out, pt)
	}
	return out, rows.Err()
}

// GetPromptTemplate returns a prompt template by ID.
func (s *Store) GetPromptTemplate(id string) (*PromptTemplate, error) {
	var pt PromptTemplate
	err := s.DB.QueryRow(
		`SELECT id, name, slug, scope, template, variables, priority, is_builtin, created_at, updated_at
		 FROM prompt_templates WHERE id = ?`, id,
	).Scan(&pt.ID, &pt.Name, &pt.Slug, &pt.Scope, &pt.Template,
		&pt.Variables, &pt.Priority, &pt.IsBuiltin, &pt.CreatedAt, &pt.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get prompt template %s: %w", id, err)
	}
	return &pt, nil
}

// GetPromptTemplateBySlug returns a prompt template by slug.
func (s *Store) GetPromptTemplateBySlug(slug string) (*PromptTemplate, error) {
	var pt PromptTemplate
	err := s.DB.QueryRow(
		`SELECT id, name, slug, scope, template, variables, priority, is_builtin, created_at, updated_at
		 FROM prompt_templates WHERE slug = ?`, slug,
	).Scan(&pt.ID, &pt.Name, &pt.Slug, &pt.Scope, &pt.Template,
		&pt.Variables, &pt.Priority, &pt.IsBuiltin, &pt.CreatedAt, &pt.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get prompt template by slug %s: %w", slug, err)
	}
	return &pt, nil
}

// CreatePromptTemplate inserts a new prompt template.
func (s *Store) CreatePromptTemplate(pt *PromptTemplate) error {
	if pt.ID == "" {
		pt.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if pt.Variables == "" {
		pt.Variables = "[]"
	}

	_, err := s.DB.Exec(
		`INSERT INTO prompt_templates (id, name, slug, scope, template, variables, priority, is_builtin, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		pt.ID, pt.Name, pt.Slug, pt.Scope, pt.Template, pt.Variables, pt.Priority, pt.IsBuiltin,
		now, now,
	)
	if err != nil {
		return fmt.Errorf("create prompt template: %w", err)
	}
	pt.CreatedAt = now
	pt.UpdatedAt = now
	return nil
}

// UpdatePromptTemplate updates a prompt template's mutable fields.
func (s *Store) UpdatePromptTemplate(pt *PromptTemplate) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(
		`UPDATE prompt_templates SET name = ?, slug = ?, scope = ?, template = ?,
		        variables = ?, priority = ?, updated_at = ?
		 WHERE id = ?`,
		pt.Name, pt.Slug, pt.Scope, pt.Template, pt.Variables, pt.Priority,
		now, pt.ID,
	)
	if err != nil {
		return fmt.Errorf("update prompt template: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("prompt template %q not found", pt.ID)
	}
	pt.UpdatedAt = now
	return nil
}

// DeletePromptTemplate removes a prompt template by ID (only non-builtin).
func (s *Store) DeletePromptTemplate(id string) error {
	res, err := s.DB.Exec(
		`DELETE FROM prompt_templates WHERE id = ? AND is_builtin = 0`, id,
	)
	if err != nil {
		return fmt.Errorf("delete prompt template %s: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("prompt template %q not found or is built-in", id)
	}
	return nil
}

// ListPromptTemplatesForAgent returns prompt templates assigned to an agent, ordered by priority.
func (s *Store) ListPromptTemplatesForAgent(agentID string) ([]PromptTemplate, error) {
	rows, err := s.DB.Query(
		`SELECT pt.id, pt.name, pt.slug, pt.scope, pt.template, pt.variables, pt.priority,
		        pt.is_builtin, pt.created_at, pt.updated_at
		 FROM prompt_templates pt
		 JOIN agent_prompt_templates apt ON pt.id = apt.template_id
		 WHERE apt.agent_id = ?
		 ORDER BY pt.priority, pt.name`, agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list prompt templates for agent: %w", err)
	}
	defer rows.Close()

	out := make([]PromptTemplate, 0)
	for rows.Next() {
		var pt PromptTemplate
		if err := rows.Scan(&pt.ID, &pt.Name, &pt.Slug, &pt.Scope, &pt.Template,
			&pt.Variables, &pt.Priority, &pt.IsBuiltin, &pt.CreatedAt, &pt.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan agent prompt template: %w", err)
		}
		out = append(out, pt)
	}
	return out, rows.Err()
}

// AssignPromptTemplateToAgent links a prompt template to an agent.
func (s *Store) AssignPromptTemplateToAgent(agentID, templateID string) error {
	_, err := s.DB.Exec(
		`INSERT OR IGNORE INTO agent_prompt_templates (agent_id, template_id)
		 VALUES (?, ?)`,
		agentID, templateID,
	)
	if err != nil {
		return fmt.Errorf("assign prompt template to agent: %w", err)
	}
	return nil
}

// RemovePromptTemplateFromAgent removes a prompt template assignment.
func (s *Store) RemovePromptTemplateFromAgent(agentID, templateID string) error {
	res, err := s.DB.Exec(
		`DELETE FROM agent_prompt_templates WHERE agent_id = ? AND template_id = ?`,
		agentID, templateID,
	)
	if err != nil {
		return fmt.Errorf("remove prompt template from agent: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("prompt template assignment not found")
	}
	return nil
}

// ComposePromptForAgent assembles the system prompt for an agent by merging all assigned
// prompt templates in priority order, resolving {{var}} placeholders from the variables map.
func (s *Store) ComposePromptForAgent(agentID string, variables map[string]string) (string, error) {
	templates, err := s.ListPromptTemplatesForAgent(agentID)
	if err != nil {
		return "", err
	}

	if len(templates) == 0 {
		return "", nil
	}

	// Sort by priority (already done in query, but defensive).
	sort.Slice(templates, func(i, j int) bool {
		return templates[i].Priority < templates[j].Priority
	})

	var parts []string
	for _, pt := range templates {
		resolved := pt.Template
		for k, v := range variables {
			resolved = strings.ReplaceAll(resolved, "{{"+k+"}}", v)
		}
		parts = append(parts, resolved)
	}

	return strings.Join(parts, "\n\n"), nil
}

// BuiltinPromptTemplates defines the default prompt templates.
var BuiltinPromptTemplates = []PromptTemplate{
	{
		Name:     "Base Identity",
		Slug:     "base-identity",
		Scope:    "system",
		Priority: 10,
		Template: `You are {{agent_name}}, {{agent_description}}.`,
		Variables: `["agent_name","agent_description"]`,
	},
	{
		Name:     "Workspace Context",
		Slug:     "workspace-context",
		Scope:    "context",
		Priority: 20,
		Template: `You are operating within the workspace "{{workspace_name}}": {{workspace_description}}.`,
		Variables: `["workspace_name","workspace_description"]`,
	},
	{
		Name:     "Project Context",
		Slug:     "project-context",
		Scope:    "context",
		Priority: 30,
		Template: `Current project: {{project_name}}. {{project_description}}`,
		Variables: `["project_name","project_description"]`,
	},
	{
		Name:     "Mode Addendum",
		Slug:     "mode-addendum",
		Scope:    "mode",
		Priority: 40,
		Template: `{{mode_addendum}}`,
		Variables: `["mode_addendum"]`,
	},
	{
		Name:     "Tool Awareness",
		Slug:     "tool-awareness",
		Scope:    "skill",
		Priority: 50,
		Template: `You have access to the following skills and their tools:
{{skill_list}}

Use these tools when appropriate to accomplish tasks. Each skill provides specific capabilities that you can invoke.`,
		Variables: `["skill_list"]`,
	},
}

// SeedBuiltinPromptTemplates inserts built-in prompt templates if they don't exist.
func (s *Store) SeedBuiltinPromptTemplates() error {
	now := time.Now().UTC().Format(time.RFC3339)
	for _, pt := range BuiltinPromptTemplates {
		_, err := s.DB.Exec(
			`INSERT OR IGNORE INTO prompt_templates (id, name, slug, scope, template, variables, priority, is_builtin, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
			uuid.New().String(), pt.Name, pt.Slug, pt.Scope, pt.Template, pt.Variables, pt.Priority, now, now,
		)
		if err != nil {
			return fmt.Errorf("seed prompt template %s: %w", pt.Slug, err)
		}
	}
	return nil
}
