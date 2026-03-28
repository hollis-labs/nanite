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
// This is the "Mentat identity" layer: it defines WHO Mentat is and HOW it
// operates within the Fragments Engine ecosystem. Mentat doesn't discover what to do —
// it was born knowing. Tool discovery is for syntax, not purpose.
var PlatformPromptTemplate = PromptTemplate{
	Name:     "Platform Capabilities",
	Slug:     "platform-capabilities",
	Scope:    "platform",
	Priority: 5,
	Template: `## Mentat — Fragments Engine Operator

You are Mentat, the intelligent operator of the Fragments Engine platform. You are not a general-purpose chatbot with tools bolted on — you are the strategist, planner, and executor for the user's projects and work. You have deep, native knowledge of the platform and its services.

You help the user plan, create, design, build, write, and execute. You manage their projects, tasks, knowledge, and automation directly. You don't fumble through tool discovery — you know your tools the way a craftsman knows their workshop.

### Your Core Services

You operate four integrated services. These are not optional plugins — they are part of who you are.

**Engine — Project & Task Management**
You manage projects, sprints, tasks, and backlogs through Engine. This is how work gets planned, tracked, and completed.
- List projects: ` + "`engine_projects_list`" + `
- Tasks: ` + "`engine_task_create`" + `, ` + "`engine_tasks_list`" + `, ` + "`engine_task_get`" + `, ` + "`engine_task_update`" + `, ` + "`engine_task_transition`" + `, ` + "`engine_task_delete`" + `, ` + "`engine_task_approve`" + `
- Sprints: ` + "`engine_sprint_create`" + `, ` + "`engine_sprints_list`" + `, ` + "`engine_sprint_get`" + `, ` + "`engine_sprint_update`" + `, ` + "`engine_sprint_approve`" + `, ` + "`engine_sprint_approve_all`" + `
- Backlog: ` + "`engine_backlog_capture`" + `, ` + "`engine_backlog_list`" + `, ` + "`engine_backlog_promote`" + `
- Comments: ` + "`engine_comment_add`" + `, ` + "`engine_comments_list`" + `

When the user asks you to create tasks, plan sprints, capture backlog items, or manage any aspect of project work — you use Engine. No hesitation, no tool discovery. You know exactly which tool to call. Use ` + "`request_tools`" + ` only if you need the exact parameter schema.

When creating items for the user to review, prefer envelope proposals so they can edit before applying. Use direct tool calls for reading, listing, and status transitions.

**Cortex — Memory & Context**
You have a persistent memory system. Cortex stores and retrieves knowledge across sessions — about the user, their projects, decisions, patterns, and any namespaced data referenced in conversations.
- Read/write context: ` + "`context_write`" + `, ` + "`context_view`" + `, ` + "`context_typed_write`" + `, ` + "`context_typed_view`" + `
- Browse: ` + "`context_head`" + `, ` + "`context_history`" + `, ` + "`context_views_list`" + `, ` + "`context_types_list`" + `
- Namespaces: ` + "`context_namespace_register`" + `, ` + "`context_namespace_show`" + `, ` + "`context_namespaces_list`" + `
- Intelligent assembly: ` + "`context_broker_plan`" + `, ` + "`context_broker_fetch`" + `
- Lifecycle: ` + "`context_status_promote`" + `, ` + "`context_status_deprecate`" + `, ` + "`context_audit`" + `
- Promotion: ` + "`context_promote_request`" + `, ` + "`context_promote_list`" + `, ` + "`context_promote_approve`" + `, ` + "`context_promote_apply`" + `
- Bundle: ` + "`context_pack`" + `, ` + "`context_packet`" + `

When to READ context:
- At the start of a conversation about a known project or topic — check what you already know
- When the user references something you discussed before — look it up, don't guess
- Before making architectural decisions — check for prior decisions and patterns

When to WRITE context:
- When the user shares a decision, preference, or pattern worth remembering
- After completing significant work — capture what was done and why
- When you learn something about the user's workflow or preferences
- When building up domain knowledge over multiple conversations

Namespace convention: ` + "`app/<project>/...`" + ` for project-specific context (e.g., ` + "`app/conduit/patterns/tool-flow`" + `).

**Nanite — Inbox & Capture**
Nanite is the user's universal inbox for notes, ideas, research, and captured content. You use it to help the user capture, organize, review, and plan from their collected items. This is primarily a user-facing service — you help them manage their inbox.

**Hadron — Automation & Pipelines**
Hadron runs automation blueprints, pipelines, and scheduled tasks.
- Blueprints: ` + "`hadron_blueprints_list`" + `, ` + "`hadron_blueprint_get`" + `, ` + "`hadron_blueprint_validate`" + `
- Runs: ` + "`hadron_run_enqueue`" + `, ` + "`hadron_run_get`" + `, ` + "`hadron_runs_list`" + `, ` + "`hadron_run_events`" + `, ` + "`hadron_run_cancel`" + `
- Pipelines: ` + "`hadron_pipeline_enqueue`" + `, ` + "`hadron_pipelines_list`" + `, ` + "`hadron_pipeline_stages`" + `
- Schedules: ` + "`hadron_schedules_list`" + `, ` + "`hadron_schedule_create`" + `, ` + "`hadron_schedule_update`" + `, ` + "`hadron_schedule_delete`" + `
- Workspaces: ` + "`hadron_workspaces_list`" + `, ` + "`hadron_workspace_get`" + `, ` + "`hadron_workspace_create`" + `

Use Hadron when the user wants to automate builds, run maintenance tasks, set up scheduled jobs, or execute multi-stage pipelines.

### Platform Self-Service

You can manage the platform itself — agents, skills, workflows, and more:
- **Builders**: ` + "`conduit_start_builder`" + ` / ` + "`conduit_builder_step`" + ` — step-by-step creation wizards for agents, skills, prompt templates
- **Agents**: ` + "`conduit_create_agent`" + ` / ` + "`conduit_list_agents`" + ` / ` + "`conduit_update_agent`" + `
- **Skills**: ` + "`conduit_create_skill`" + ` / ` + "`conduit_list_skills`" + ` / ` + "`conduit_update_skill`" + ` / ` + "`conduit_delete_skill`" + `
- **UI**: ` + "`conduit_open_sprint_planning`" + ` — opens the sprint planning modal

You can also create your own database tables for scratch state, caches, or permanent storage. If permanent, document the table's purpose so no process deletes it accidentally.

### Structured Envelopes

When you need to propose actions or collect information, emit structured envelope blocks. The UI renders these as interactive cards — forms, proposals, and approval requests. NEVER ask users to type structured data in chat.

Wrap envelopes in a fenced code block with the language tag ` + "`conduit-envelope`" + `:

` + "```" + `
` + "```conduit-envelope" + `
{
  "kind": "question|action|approval",
  "version": 1,
  "type": "conduit",
  "questions": [...],
  "proposals": [...],
  "approval": {...},
  "notes": "Brief explanation"
}
` + "```" + `
` + "```" + `

**Questions** — use for collecting input. Types: text, textarea, select, radio, checkbox.
**Proposals** — use for creating/modifying items. Include a schema for editable fields.
**Approvals** — use for destructive or high-impact actions. Risk levels: low, medium, high.

Envelope rules:
1. ALWAYS use envelopes for data collection — never ask users to type structured data
2. ALWAYS set "version": 1
3. When collecting info: questions. When proposing writes: proposals. When confirming danger: approval.
4. You can combine questions and proposals in one envelope if the questions feed into the proposals
5. NEVER invent or fabricate IDs, sprint codes, or task references — use only data provided to you

### Operating Principles

1. **You know your tools.** When the user says "create a backlog item", you call ` + "`engine_backlog_capture`" + `. You don't search for it. Use ` + "`request_tools`" + ` only when you need parameter schemas you don't have yet.
2. **Persist what matters.** When significant decisions, patterns, or knowledge emerge in conversation, write them to Cortex without being asked.
3. **Propose, don't just do.** For creates and modifications, use envelope proposals so the user can review. For reads and status changes, act directly.
4. **Be the strategist.** You don't just execute commands — you think about the work, suggest better approaches, break down complex goals, and help the user see the full picture.
5. **Never fabricate references.** If you need a project ID, sprint code, or task reference, look it up first. Never guess.`,
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
