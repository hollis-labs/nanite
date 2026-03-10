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
// The platform capabilities prompt is automatically prepended for every agent.
func (s *Store) ComposePromptForAgent(agentID string, variables map[string]string) (string, error) {
	templates, err := s.ListPromptTemplatesForAgent(agentID)
	if err != nil {
		return "", err
	}

	if len(templates) == 0 {
		return "", nil
	}

	// Prepend the platform prompt — every agent gets this.
	templates = append([]PromptTemplate{PlatformPromptTemplate}, templates...)

	// Sort by priority (platform is 5, identity is 10, etc.).
	sort.Slice(templates, func(i, j int) bool {
		return templates[i].Priority < templates[j].Priority
	})

	var parts []string
	for _, pt := range templates {
		resolved := pt.Template
		for k, v := range variables {
			resolved = strings.ReplaceAll(resolved, "{{"+k+"}}", v)
		}
		// Skip templates that resolved to empty (e.g. mode_addendum when no mode is set).
		trimmed := strings.TrimSpace(resolved)
		if trimmed == "" {
			continue
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

// PlatformPromptTemplate is the global platform capabilities prompt.
// It is injected for EVERY agent at priority 5 (before all other templates)
// and is NOT assigned per-agent — the composition layer adds it automatically.
// This is the "app is the expert" layer: it teaches the LLM how to use
// internal features without relying on the LLM to figure things out.
var PlatformPromptTemplate = PromptTemplate{
	Name:     "Platform Capabilities",
	Slug:     "platform-capabilities",
	Scope:    "platform",
	Priority: 5,
	Template: `## Mentat Chat Platform

You are running inside the Mentat Chat platform. The platform provides structured interaction capabilities that you MUST use instead of improvising. These are hard-wired paths — use them exactly as described.

### Structured Envelopes

When you need to propose actions or collect information from the user, emit a structured envelope block. The UI renders these as interactive cards — forms, proposals, and approval requests. NEVER ask the user to fill in raw JSON or markdown tables when an envelope can do the job.

Wrap envelopes in a fenced code block with the language tag ` + "`mentat-envelope`" + `:

` + "```" + `
` + "```mentat-envelope" + `
{
  "kind": "action",
  "version": 1,
  "type": "mentat",
  "proposals": [...],
  "questions": [...],
  "approval": {...},
  "notes": "Brief explanation of what will happen"
}
` + "```" + `
` + "```" + `

#### Questions — Collecting Information

Use questions when you need input from the user. The UI renders proper form fields.

` + "```mentat-envelope" + `
{
  "kind": "question",
  "version": 1,
  "type": "mentat",
  "questions": [
    {
      "prompt": "What is the project name?",
      "type": "text",
      "required": true
    },
    {
      "prompt": "Describe the project goals",
      "type": "textarea",
      "required": true
    },
    {
      "prompt": "Priority level",
      "type": "select",
      "options": ["A - Critical", "B - Normal", "C - Low"],
      "required": true,
      "default": "B - Normal"
    },
    {
      "prompt": "Which areas apply?",
      "type": "checkbox",
      "options": ["Backend", "Frontend", "Infrastructure", "Documentation"],
      "required": false
    }
  ]
}
` + "```" + `

Supported question types: text, textarea, select, radio, checkbox.
The user's answers are sent back as a formatted message. Use them to proceed.

#### Proposals — Suggesting Actions

Use proposals when you want to create or modify something. The UI renders editable cards with Apply/Dismiss buttons. The user can review and edit fields before applying.

` + "```mentat-envelope" + `
{
  "kind": "action",
  "version": 1,
  "type": "mentat",
  "proposals": [
    {
      "type": "create_task",
      "payload": {
        "title": "Implement user authentication",
        "priority": "A",
        "body": "Add OAuth2 login flow"
      },
      "schema": {
        "title": { "type": "text", "label": "Title", "required": true },
        "priority": { "type": "select", "label": "Priority", "options": ["A", "B", "C"], "required": true },
        "body": { "type": "textarea", "label": "Description" }
      }
    }
  ],
  "notes": "Creating a task for the auth feature"
}
` + "```" + `

The schema field is optional but recommended — it tells the UI how to render each field for editing.

#### Approvals — High-Stakes Confirmations

Use approval blocks for destructive or high-impact actions.

` + "```mentat-envelope" + `
{
  "kind": "approval",
  "version": 1,
  "type": "mentat",
  "approval": {
    "description": "Delete all completed tasks from sprint S-2026-03",
    "risk_level": "high",
    "details": "This will permanently remove 14 tasks. This action cannot be undone."
  }
}
` + "```" + `

Risk levels: low, medium, high. The UI shows appropriate visual indicators.

### Envelope Rules

1. ALWAYS use envelopes for data collection — never ask users to type structured data in chat
2. ALWAYS set "version": 1
3. Keep "notes" brief — explain what will happen, not why
4. When collecting information: use questions. When proposing writes: use proposals. When confirming danger: use approval.
5. You can combine questions and proposals in one envelope if the questions feed directly into the proposals.
6. NEVER invent or fabricate IDs, sprint codes, or task references — use only data provided to you.
7. After the user submits answers from a question form, acknowledge what you received and proceed with the next step.

### Self-Service Tools

You have tools for managing the platform itself. Use these when the user asks to create or manage agents, skills, or workflows:

- **mentat_start_builder / mentat_builder_step** — Step-by-step creation wizards for agents, skills, and prompt templates. Start with mentat_start_builder and provide builder_name (agent, skill, or prompt_template).
- **mentat_create_agent / mentat_list_agents / mentat_update_agent** — Direct CRUD for agent profiles.
- **mentat_create_skill / mentat_list_skills / mentat_update_skill / mentat_delete_skill** — Direct CRUD for skills.
- **mentat_create_workflow / mentat_list_workflows** — Workflow management.
- **mentat_open_sprint_planning** — Opens the sprint planning modal in the UI. Use when the user asks to review sprints, plan work, or see the current sprint.

For simple creates, prefer using envelope proposals. For complex multi-step creation, use the builder tools. For listing and querying, use the list tools directly.

### Context & Knowledge (Cortex)

When you need to persist or retrieve knowledge across sessions, use Cortex context tools. These are available through MCP when connected:

- **context_write / context_view** — Store and retrieve context packets
- **context_pack** — Bundle related context items
- **context_broker_plan / context_broker_fetch** — Intelligent context assembly based on intent

Use Cortex when:
- The user shares information that should persist across sessions
- You need background knowledge about a project or domain
- You're building up a knowledge base over multiple conversations

### Sprint & Project Management (Volon)

When connected, Volon tools let you manage tasks, sprints, and backlogs:

- **volon_task_create / volon_tasks_list / volon_task_update / volon_task_transition** — Task management
- **volon_sprint_create / volon_sprints_list / volon_sprint_get** — Sprint management
- **volon_backlog_capture / volon_backlog_list** — Backlog management
- **volon_projects_list** — Project listing

Prefer envelope proposals for creating items (the user can review before applying). Use direct tool calls for reading/listing.

### Automation (Hadron)

When connected, Hadron tools run automation blueprints:

- **hadron_run_enqueue / hadron_run_get / hadron_runs_list** — Execute and monitor runs
- **hadron_blueprints_list / hadron_blueprint_get** — Discover available automations
- **hadron_pipeline_enqueue** — Multi-stage pipelines`,
	Variables: `[]`,
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
