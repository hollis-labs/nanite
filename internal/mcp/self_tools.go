package mcp

import "github.com/hollis-labs/go-providers/provider"

// SelfToolProviderDefinitions returns all self-service tool definitions
// in provider.ToolDefinition format, suitable for registering as built-ins.
func SelfToolProviderDefinitions() []provider.ToolDefinition {
	tools := selfToolDefinitions()
	defs := make([]provider.ToolDefinition, len(tools))
	for i, t := range tools {
		defs[i] = provider.ToolDefinition{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
	}
	return defs
}

// selfToolDefinitions returns all self-service tool definitions.
func selfToolDefinitions() []Tool {
	return []Tool{
		{
			Name: "nanite_create_skill",
			Description: "Create a new skill that binds a set of tool names to a named category.\n\n" +
				"**When to use:** When the user asks to define a new skill, workflow, or named capability that groups related tools.\n\n" +
				"**When NOT to use:** Do not create duplicate slugs — use nanite_update_skill to modify an existing one.\n\n" +
				"**Output shape:** \"Created skill <name> (<id>)\" on success. Use nanite_list_skills to verify afterward.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":          map[string]any{"type": "string", "description": "Human-readable skill name"},
					"slug":          map[string]any{"type": "string", "description": "URL-safe slug (unique)"},
					"description":   map[string]any{"type": "string", "description": "What this skill does"},
					"category":      map[string]any{"type": "string", "description": "Category (e.g. dev, general, custom)"},
					"tool_bindings": map[string]any{"type": "string", "description": "JSON array of tool names this skill binds"},
					"input_schema":  map[string]any{"type": "string", "description": "JSON schema for skill inputs (optional)"},
				},
				"required": []string{"name", "slug", "description"},
			},
		},
		{
			Name: "nanite_list_skills",
			Description: "List all skills, optionally filtered by category.\n\n" +
				"**When to use:** When the user asks what skills are available, or before creating a skill to check for duplicates.\n\n" +
				"**Output shape:** Text list of skills with name, slug, category, and description. Empty list if none match the filter.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"category": map[string]any{"type": "string", "description": "Filter by category (optional)"},
				},
			},
		},
		{
			Name: "nanite_update_skill",
			Description: "Update an existing skill by ID. Only the fields you provide are changed (partial update).\n\n" +
				"**When to use:** When the user asks to rename, re-categorize, or change the tool bindings of an existing skill.\n\n" +
				"**Required context:** You need the skill ID — get it from nanite_list_skills first if you only have the name or slug.\n\n" +
				"**Output shape:** \"Updated skill <id>\" on success.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":            map[string]any{"type": "string", "description": "Skill ID to update"},
					"name":          map[string]any{"type": "string", "description": "New name (optional)"},
					"slug":          map[string]any{"type": "string", "description": "New slug (optional)"},
					"description":   map[string]any{"type": "string", "description": "New description (optional)"},
					"category":      map[string]any{"type": "string", "description": "New category (optional)"},
					"tool_bindings": map[string]any{"type": "string", "description": "New tool bindings JSON array (optional)"},
					"input_schema":  map[string]any{"type": "string", "description": "New input schema JSON (optional)"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name: "nanite_delete_skill",
			Description: "Permanently delete a skill by ID. Irreversible. Only non-builtin skills can be deleted.\n\n" +
				"**When to use:** When the user explicitly asks to remove a custom skill.\n\n" +
				"**Required context:** You need the skill ID — get it from nanite_list_skills if you only have the name.\n\n" +
				"**Output shape:** \"Deleted skill <id>\" on success. Returns an error if the skill is builtin or not found.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "Skill ID to delete"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name: "nanite_create_agent",
			Description: "Create a new agent profile with a name, slug, system prompt, and optional default model.\n\n" +
				"**When to use:** When the user asks to define a new agent persona or role.\n\n" +
				"**When NOT to use:** Do not create duplicate slugs — use nanite_update_agent to modify an existing profile.\n\n" +
				"**Output shape:** \"Created agent <name> (<id>)\" on success. Use nanite_list_agents to verify.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":          map[string]any{"type": "string", "description": "Agent display name"},
					"slug":          map[string]any{"type": "string", "description": "URL-safe slug (unique)"},
					"system_prompt": map[string]any{"type": "string", "description": "System prompt for the agent"},
					"description":   map[string]any{"type": "string", "description": "Short description (optional)"},
					"default_model": map[string]any{"type": "string", "description": "Default LLM model (optional)"},
				},
				"required": []string{"name", "slug", "system_prompt"},
			},
		},
		{
			Name: "nanite_list_agents",
			Description: "List all agent profiles.\n\n" +
				"**When to use:** When the user asks what agents are configured, or before creating one to check for name/slug conflicts.\n\n" +
				"**Output shape:** Text list of agents with name, slug, description, and default model. Returns all agents — no filter parameters.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name: "nanite_update_agent",
			Description: "Update an existing agent profile by ID. Only the fields you provide are changed (partial update).\n\n" +
				"**When to use:** When the user asks to change an agent's system prompt, name, slug, or default model.\n\n" +
				"**Required context:** You need the agent ID — get it from nanite_list_agents first if you only have the slug.\n\n" +
				"**Output shape:** \"Updated agent <id>\" on success.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":            map[string]any{"type": "string", "description": "Agent ID to update"},
					"name":          map[string]any{"type": "string", "description": "New name (optional)"},
					"slug":          map[string]any{"type": "string", "description": "New slug (optional)"},
					"system_prompt": map[string]any{"type": "string", "description": "New system prompt (optional)"},
					"description":   map[string]any{"type": "string", "description": "New description (optional)"},
					"default_model": map[string]any{"type": "string", "description": "New default model (optional)"},
				},
				"required": []string{"id"},
			},
		},
		// Cross-app navigation tools — control Engine GUI via SSE
		{
			Name:        "nanite_navigate_engine",
			Description: "Navigate the Engine GUI to a specific page with optional filters. Use when the user asks to see tasks, sprints, kanban, or any Engine view. The Engine GUI will navigate in real-time via SSE. You can apply filters like project, status, priority, and sort order.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"page": map[string]any{
						"type":        "string",
						"description": "Page to navigate to: ops-dashboard, tasks, task-detail, sprints, sprint-detail, kanban, epics, projects, activity, inspector",
					},
					"id": map[string]any{
						"type":        "string",
						"description": "Entity ID for detail pages. Must be an ID returned by a prior tool call in this turn — do NOT invent IDs. Optional; omit for list pages.",
					},
					"project_id": map[string]any{
						"type":        "string",
						"description": "Filter by project ID (e.g. 'nanite', 'engine', 'conduit'). Optional.",
					},
					"status": map[string]any{
						"type":        "string",
						"description": "Filter by status. Comma-separated for multiple (e.g. 'todo,doing' or 'blocked'). Optional.",
					},
					"priority": map[string]any{
						"type":        "string",
						"description": "Filter by priority: 1, 2, or 3 (maps to P1/P2/P3). Comma-separated for multiple. Optional.",
					},
					"sort": map[string]any{
						"type":        "string",
						"description": "Sort order: 'priority', 'status', 'created', 'updated', 'title'. Prefix with '-' for descending (e.g. '-priority'). Optional.",
					},
				},
				"required": []string{"page"},
			},
		},
		{
			Name:        "nanite_refresh_engine",
			Description: "Trigger a data refresh in the Engine GUI. Use after making changes (task transitions, sprint updates) so the GUI reflects the latest state.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name: "nanite_show_giphy",
			Description: "Search Giphy for an animated GIF and display it in chat as a rich card (giphy-modal envelope).\n\n" +
				"**When to use:** When the user asks for a GIF, wants to celebrate, or the conversation tone calls for a visual reaction. This is purely cosmetic.\n\n" +
				"**When NOT to use:** Do NOT use to \"display\" real data or metrics — use nanite_show_report or nanite_show_document for that. This renders a GIF, not structured information.\n\n" +
				"**Output shape:** Emits a giphy-modal envelope; the UI renders the first matching GIF inline in chat.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "Search term (e.g. 'celebration', 'thumbs up', 'mind blown')"},
				},
				"required": []string{"query"},
			},
		},
		{
			Name: "nanite_show_document",
			Description: "Display a document in chat as a rich scrollable viewer. Use for executive summaries, reports, meeting notes, or any long-form content the user should read. " +
				"The user expects LIVE data: you MUST ground the content in data returned from previous tool calls made during this generation. Cite those tool calls in the `sources` field. Do NOT compose from memory or pattern-completion.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":             map[string]any{"type": "string", "description": "Document title"},
					"content":           map[string]any{"type": "string", "description": "Document body (HTML or markdown). Must be derived from the tool_use_ids listed in `sources`."},
					"format":            map[string]any{"type": "string", "description": "Content format: html or markdown (default: markdown)"},
					"sections":          map[string]any{"type": "string", "description": "Comma-separated section names for jump-nav (optional)"},
					"download_filename": map[string]any{"type": "string", "description": "Filename for download button (optional, e.g. report.html)"},
					"sources":           map[string]any{"type": "string", "description": "JSON array of objects documenting the grounding: [{tool_use_id, tool_name, note?}]. Each source must be a tool_use_id from a tool call in THIS generation whose result materially informs the content. Minimum 1 source. If you didn't fetch the data, don't render the document — say so in plain text instead."},
				},
				"required": []string{"title", "content", "sources"},
			},
		},
		{
			Name: "nanite_show_report",
			Description: "Display a metrics report card in chat with labeled values, progress bars, and action buttons. Use for sprint progress, portfolio health, or status summaries. " +
				"The user expects LIVE data: you MUST ground every metric and summary bullet in data returned from previous tool calls made during this generation. Cite those tool calls in the `sources` field. Do NOT compose from memory or pattern-completion — if you haven't fetched the numbers, don't render a card.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":   map[string]any{"type": "string", "description": "Report title"},
					"metrics": map[string]any{"type": "string", "description": "JSON array of metric objects: [{label, value, percent?, color?}]. Every value must come from a tool result cited in `sources`. Colors: emerald, green, amber, red, blue, violet."},
					"summary": map[string]any{"type": "string", "description": "Summary text (markdown). Must be derived from the tool_use_ids listed in `sources`. Optional."},
					"actions": map[string]any{"type": "string", "description": "JSON array of action objects: [{label, action, id?}]. Optional."},
					"sources": map[string]any{"type": "string", "description": "JSON array of objects documenting the grounding: [{tool_use_id, tool_name, note?}]. Each source must be a tool_use_id from a tool call in THIS generation whose result materially informs the report. Minimum 1 source. If you didn't fetch the data, don't render the report — say so in plain text instead."},
				},
				"required": []string{"title", "metrics", "sources"},
			},
		},
		// Builder tools — interactive step-by-step creation flows
		{
			Name: "nanite_start_builder",
			Description: "Start a step-by-step creation wizard for a new agent, skill, or prompt template.\n\n" +
				"**When to use:** When the user wants to interactively create a new agent, skill, or prompt template and you want to gather the required fields one step at a time.\n\n" +
				"**When NOT to use:** NOT for asking arbitrary questions — this builder only drives entity creation (agent / skill / prompt_template). Do not call this for read or update operations. If you already have all required fields, use nanite_create_agent or nanite_create_skill directly.\n\n" +
				"**Required context:** You MUST supply builder_name. Omit it only to list available builder types.\n\n" +
				"**Output shape:** Returns the first step prompt. Pass the response to nanite_builder_step to advance through subsequent steps.\n\n" +
				"**Chaining:** Always follow with nanite_builder_step for each subsequent step until the builder signals completion.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"builder_name": map[string]any{"type": "string", "description": "Builder to start: agent, skill, or prompt_template. Omit to list available builders."},
				},
			},
		},
		{
			Name: "nanite_builder_step",
			Description: "Submit a value for the current step in an active builder flow and advance to the next.\n\n" +
				"**When to use:** After nanite_start_builder has been called and returned the first step, call this for each step until the builder indicates completion.\n\n" +
				"**When NOT to use:** Do not call this before nanite_start_builder — there must be an active builder session for this to work.\n\n" +
				"**Output shape:** Returns the next step's prompt text, or the final entity record (JSON) on completion.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"builder_name": map[string]any{"type": "string", "description": "Name of the active builder (agent, skill, or prompt_template)"},
					"step_name":    map[string]any{"type": "string", "description": "Name of the step to submit a value for"},
					"value":        map[string]any{"type": "string", "description": "The value for this step"},
				},
				"required": []string{"builder_name", "step_name"},
			},
		},
		// --- Todo/Plan tools ---
		{
			Name: "nanite_todo_create",
			Description: "Create a todo item scoped to workspace, project, or session.\n\n" +
				"**When to use:** When the user asks to track a task, action item, or follow-up. Choose the scope that matches where the work lives:\n" +
				"- `session`: items tied to this chat conversation only (scope_id auto-filled from the current session when omitted).\n" +
				"- `project`: items that belong to a project across sessions (scope_id = project_id).\n" +
				"- `workspace`: global items visible in any project or session.\n\n" +
				"**When NOT to use:** Do not use for multi-step plans with dependencies — use nanite_plan_create for those.\n\n" +
				"**Required context:** `scope` is required. For scope=session, `scope_id` is auto-filled from the current session context if omitted; for scope=project you must supply scope_id.\n\n" +
				"**Output shape:** \"Created todo <title> (<id>)\" on success.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":       map[string]any{"type": "string", "description": "Todo title"},
					"scope":       map[string]any{"type": "string", "description": "Scope: workspace, project, or session"},
					"scope_id":    map[string]any{"type": "string", "description": "Scope ID (project_id or session_id). Auto-filled from the current chat session when scope is 'session' and this field is omitted."},
					"priority":    map[string]any{"type": "string", "description": "Priority: low, medium, high, critical (default: medium)"},
					"description": map[string]any{"type": "string", "description": "Detailed description (optional)"},
					"parent_id":   map[string]any{"type": "string", "description": "Parent todo ID for nesting (optional)"},
					"labels":      map[string]any{"type": "string", "description": "JSON array of label strings (optional)"},
				},
				"required": []string{"title", "scope"},
			},
		},
		{
			Name: "nanite_todo_update",
			Description: "Update a todo's status, priority, title, or description. Partial update — only provided fields change.\n\n" +
				"**When to use:** When the user marks a task done, blocks it, changes its priority, or renames it. Typical status transitions: pending → in_progress → done, or → blocked.\n\n" +
				"**Required context:** You need the todo ID. Get it from nanite_todo_list if you don't have it.\n\n" +
				"**Output shape:** \"Updated todo <id>\" on success.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":          map[string]any{"type": "string", "description": "Todo ID to update"},
					"title":       map[string]any{"type": "string", "description": "New title (optional)"},
					"description": map[string]any{"type": "string", "description": "New description (optional)"},
					"status":      map[string]any{"type": "string", "description": "New status: pending, in_progress, done, blocked (optional)"},
					"priority":    map[string]any{"type": "string", "description": "New priority: low, medium, high, critical (optional)"},
					"labels":      map[string]any{"type": "string", "description": "New labels JSON array (optional)"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name: "nanite_todo_list",
			Description: "List todos with optional filters, and render an interactive todo-list card when scope is provided.\n\n" +
				"**When to use:** When the user asks to see their todos, check what's pending, or view the task list for a session or project.\n\n" +
				"**Scope semantics:** Pass `scope` + `scope_id` to get a correctly scoped live card. For `scope=session`, `scope_id` is auto-filled from the current session context when omitted — you do not need to supply it explicitly. For `scope=project`, supply the project_id explicitly.\n\n" +
				"**Output shape:** Text summary of matching todos (count + titles). When `scope` is provided, also emits an interactive todo-list envelope that the UI renders as a live card (lazy-fetches current data at render time — NOT the snapshot from this call). Do NOT emit a nanite-envelope block manually — this tool handles that automatically.\n\n" +
				"**When NOT to use:** Do not call without `scope` if you want the interactive card — a scopeless call returns text only and emits no card.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scope":    map[string]any{"type": "string", "description": "Filter by scope: workspace, project, or session. Required to render an interactive card."},
					"scope_id": map[string]any{"type": "string", "description": "Scope ID (project_id or session_id). Auto-filled from the current chat session when scope is 'session' and this field is omitted."},
					"status":   map[string]any{"type": "string", "description": "Filter by status: pending, in_progress, done, blocked (optional)"},
					"priority": map[string]any{"type": "string", "description": "Filter by priority: low, medium, high, critical (optional)"},
					"title":    map[string]any{"type": "string", "description": "Title shown at the top of the interactive card (optional, defaults to \"Todos\")"},
				},
			},
		},
		{
			Name: "nanite_plan_create",
			Description: "Create a multi-step plan with ordered phases, dependencies, and acceptance criteria.\n\n" +
				"**When to use:** When the user describes a multi-step or phased deliverable where order and dependencies matter — code migrations, feature rollouts, structured workflows. Prefer plans over todos when steps have depends_on relationships or acceptance criteria.\n\n" +
				"**When NOT to use:** For simple unordered checklists, use nanite_todo_create instead. Do not create a plan for a single action.\n\n" +
				"**Scope semantics:** Same three-tier scope as todos (workspace / project / session). For `scope=session`, `scope_id` is auto-filled from the current session context when omitted.\n\n" +
				"**After creating a plan with status 'proposed'**, emit a plan-review envelope so the user can approve or reject inline:\n" +
				"```nanite-envelope\n{\"kind\":\"envelope\",\"version\":1,\"type\":\"plan-review\",\"data\":{\"plan_id\":\"...\",\"title\":\"...\",\"description\":\"...\",\"status\":\"proposed\",\"steps\":[{\"id\":\"...\",\"title\":\"...\"}]}}\n```\n\n" +
				"**Output shape:** \"Created plan <title> (<id>)\" on success. Use nanite_plan_get to retrieve the full plan with step IDs.\n\n" +
				"**Chaining:** Follow with nanite_plan_update(step_id=...) to advance individual step statuses as work progresses.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":       map[string]any{"type": "string", "description": "Plan title"},
					"scope":       map[string]any{"type": "string", "description": "Scope: workspace, project, or session"},
					"scope_id":    map[string]any{"type": "string", "description": "Scope ID (project_id or session_id). Auto-filled from the current chat session when scope is 'session' and this field is omitted."},
					"description": map[string]any{"type": "string", "description": "Plan description (optional)"},
					"steps":       map[string]any{"type": "string", "description": "JSON array of step objects: [{id, title, status, depends_on, acceptance, notes}]"},
				},
				"required": []string{"title", "scope"},
			},
		},
		{
			Name: "nanite_plan_update",
			Description: "Update a plan's top-level fields or transition a single step's status.\n\n" +
				"**When to use:** To advance a step as work progresses (e.g. pending → in_progress → done), or to rename/re-status the plan itself.\n\n" +
				"**Required context:** Always supply the plan `id`. Supply `step_id` to update only that step; omit it to update plan-level fields.\n\n" +
				"**Plan status transitions:** proposed → approved → in_progress → complete (or abandoned).\n" +
				"**Step status transitions:** pending → in_progress → done (or skipped).\n\n" +
				"**Output shape:** \"Updated plan <id>\" or \"Updated step <step_id> in plan <id>\" on success.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":      map[string]any{"type": "string", "description": "Plan ID to update"},
					"step_id": map[string]any{"type": "string", "description": "Step ID to update (optional — if provided, updates just that step)"},
					"title":   map[string]any{"type": "string", "description": "New plan title (optional, ignored if step_id set)"},
					"status":  map[string]any{"type": "string", "description": "New status. Plan: proposed/approved/in_progress/complete/abandoned. Step: pending/in_progress/done/skipped."},
					"notes":   map[string]any{"type": "string", "description": "Notes for the step (optional, only with step_id)"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name: "nanite_plan_list",
			Description: "List plans, optionally filtered by scope, scope_id, and/or status.\n\n" +
				"**When to use:** When the user asks what plans are active, or before creating a plan to check for duplicates. For scope=session, scope_id is auto-filled when omitted.\n\n" +
				"**Output shape:** Text list of matching plans with title, status, scope, and step count. Use nanite_plan_get for full step detail.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scope":    map[string]any{"type": "string", "description": "Filter by scope: workspace, project, or session (optional)"},
					"scope_id": map[string]any{"type": "string", "description": "Scope ID (project_id or session_id). Auto-filled from the current chat session when scope is 'session' and this field is omitted."},
					"status":   map[string]any{"type": "string", "description": "Filter by status: proposed, approved, in_progress, complete, abandoned (optional)"},
				},
			},
		},
		{
			Name: "nanite_plan_get",
			Description: "Get a single plan by ID, including all steps with their IDs, statuses, and dependencies.\n\n" +
				"**When to use:** After nanite_plan_create or nanite_plan_list to retrieve step IDs needed for nanite_plan_update(step_id=...).\n\n" +
				"**Output shape:** Full plan as JSON — {id, title, scope, scope_id, status, description, steps: [{id, title, status, depends_on, acceptance, notes}]}.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "Plan ID"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name: "nanite_plan_delete",
			Description: "Permanently delete a plan by ID, including all its steps. Irreversible.\n\n" +
				"**When to use:** When the user explicitly discards a plan they no longer need.\n\n" +
				"**Required context:** You need the plan ID — get it from nanite_plan_list if you don't have it.\n\n" +
				"**Output shape:** \"Deleted plan <id>\" on success.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "Plan ID to delete"},
				},
				"required": []string{"id"},
			},
		},
		// --- Install tools ---
		{
			Name: "nanite_install_home",
			Description: "Extract the embedded Nanite framework assets (roles, skills, config templates) to ~/.nanite/.\n\n" +
				"**When to use:** On first install or to update framework assets after a Nanite upgrade.\n\n" +
				"**When NOT to use:** Do not use with force=true unless the user explicitly asks — it overwrites user-modified files.\n\n" +
				"**Output shape:** Summary of files written/skipped. Use force=true only to reset user-modified files to bundled defaults.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"force": map[string]any{"type": "boolean", "description": "overwrite user-modified files"},
				},
			},
		},
		{
			Name: "nanite_install_project",
			Description: "Scaffold .nanite/ and NANITE.md in a project directory, optionally migrating from a legacy .agentrc/ layout.\n\n" +
				"**When to use:** When onboarding a new project to Nanite, or when migrating from the older .agentrc/ convention.\n\n" +
				"**Required context:** project_dir must be an absolute path to the project root.\n\n" +
				"**Output shape:** Summary of files created. Use nanite_install_rollback to undo if needed.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project_dir":          map[string]any{"type": "string", "description": "absolute path to project dir"},
					"migrate_from_agentrc": map[string]any{"type": "boolean", "description": "archive .agentrc/ and migrate"},
					"archive_only":         map[string]any{"type": "boolean", "description": "archive .agentrc/ without scaffolding"},
				},
				"required": []string{"project_dir"},
			},
		},
		{
			Name: "nanite_install_rollback",
			Description: "Reverse the most recent nanite_install_project migration for a project, restoring from its archive snapshot.\n\n" +
				"**When to use:** When an install or migration went wrong and the user wants to restore the previous state.\n\n" +
				"**Required context:** project_dir is required. archive_path is optional — if omitted, the most recent archive snapshot is used.\n\n" +
				"**Output shape:** Summary of files restored.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project_dir":  map[string]any{"type": "string", "description": "absolute path to project dir"},
					"archive_path": map[string]any{"type": "string", "description": "explicit archive dir; finds most recent if empty"},
				},
				"required": []string{"project_dir"},
			},
		},
		{
			Name: "nanite_install_diff",
			Description: "Dry-run a project install: show what files would be created or modified without actually changing anything.\n\n" +
				"**When to use:** Before running nanite_install_project or a migration, to preview the impact.\n\n" +
				"**Note:** Not yet implemented — returns a not-implemented error.\n\n" +
				"**Output shape:** Diff summary of would-be changes (when implemented).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project_dir":          map[string]any{"type": "string", "description": "absolute path to project dir"},
					"migrate_from_agentrc": map[string]any{"type": "boolean", "description": "simulate a migration"},
				},
				"required": []string{"project_dir"},
			},
		},
		// --- messaging tools ---
		// nanite_message_subscribe is intentionally NOT registered here: it
		// requires streaming support in mcp-go or a custom server-side
		// handler, which is deferred to a follow-up task.
		{
			Name: "nanite_message_send",
			Description: "Send a message from one agent/session to another.\n\n" +
				"**When to use:** To deliver a message to the user (to_agent_id='user'), hand off context to another agent, or notify a peer session.\n\n" +
				"**Channel policy:** 'chat' for in-session conversation visible inline; 'inbox' for async work the recipient polls; 'alert' for one-off agent-triggered notifications.\n\n" +
				"**Required context:** from_session_id, from_agent_id, to_session_id, to_agent_id, and body are required. Set reply_to to the parent message ID to thread a reply.\n\n" +
				"**Output shape:** Confirmation with the new message ID. Use nanite_message_inbox to poll for replies.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"from_session_id": map[string]any{"type": "string"},
					"from_agent_id":   map[string]any{"type": "string"},
					"to_session_id":   map[string]any{"type": "string"},
					"to_agent_id":     map[string]any{"type": "string"},
					"channel":         map[string]any{"type": "string", "enum": []string{"chat", "inbox", "alert"}, "description": "Transport bucket. Defaults to 'chat' when omitted."},
					"kind":            map[string]any{"type": "string", "enum": []string{"request", "reply", "notification", "handoff"}, "description": "Wire type. Defaults to 'notification' when omitted."},
					"payload_json":    map[string]any{"type": "string", "description": "Kind-specific JSON payload (S5 ResponseV1 shape). Defaults to '{}'."},
					"subject":         map[string]any{"type": "string"},
					"body":            map[string]any{"type": "string"},
					"type":            map[string]any{"type": "string", "enum": []string{"message", "help_request", "directive", "status_update", "handoff"}},
					"reply_to":        map[string]any{"type": "string", "description": "Parent message ID to continue an existing thread."},
					"register_as":     map[string]any{"type": "string", "enum": []string{"", "external", "cli"}, "description": "Auto-register the from_agent_id on first send with this provenance kind. Ignored if the agent already exists."},
				},
				"required": []string{"from_session_id", "from_agent_id", "to_session_id", "to_agent_id", "body"},
			},
		},
		{
			Name: "nanite_message_inbox",
			Description: "Read the messaging inbox for a (session_id, agent_id) pair.\n\n" +
				"**When to use:** To poll for new messages (unread), check async replies, or review alerts. Filter by status=unread to see only new messages.\n\n" +
				"**Required context:** session_id and agent_id are required.\n\n" +
				"**Output shape:** List of messages with id, sender, channel, kind, subject, body, and status. Chain with nanite_message_ack to mark messages read.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id": map[string]any{"type": "string"},
					"agent_id":   map[string]any{"type": "string"},
					"status":     map[string]any{"type": "string", "enum": []string{"", "unread", "read", "acknowledged", "resolved"}},
					"channel":    map[string]any{"type": "string", "enum": []string{"", "chat", "inbox", "alert"}, "description": "Filter by channel; empty returns all channels."},
					"kind":       map[string]any{"type": "string", "enum": []string{"", "request", "reply", "notification", "handoff"}, "description": "Filter by wire kind; empty returns all kinds."},
				},
				"required": []string{"session_id", "agent_id"},
			},
		},
		{
			Name: "nanite_message_thread",
			Description: "Get all messages in a thread by thread_id, filtered to messages where the caller is sender or recipient.\n\n" +
				"**When to use:** When you have a thread_id from a prior message and want to see the full conversation history.\n\n" +
				"**Required context:** thread_id, session_id, and agent_id are all required. Non-participants receive an empty slice (no existence leak).\n\n" +
				"**Output shape:** Ordered list of messages in the thread visible to the caller.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"thread_id":  map[string]any{"type": "string"},
					"session_id": map[string]any{"type": "string", "description": "Caller's session id — used for participant filtering."},
					"agent_id":   map[string]any{"type": "string", "description": "Caller's agent id — used for participant filtering."},
				},
				"required": []string{"thread_id", "session_id", "agent_id"},
			},
		},
		{
			Name: "nanite_message_ack",
			Description: "Mark a message as read (acknowledged).\n\n" +
				"**When to use:** After processing a message from the inbox to clear the unread state. Idempotent — safe to call multiple times.\n\n" +
				"**Output shape:** Confirmation of updated status.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id": map[string]any{"type": "string"},
					"agent_id":   map[string]any{"type": "string"},
					"message_id": map[string]any{"type": "string"},
				},
				"required": []string{"session_id", "agent_id", "message_id"},
			},
		},
		{
			Name: "nanite_message_resolve",
			Description: "Mark a message as resolved (work completed on this message).\n\n" +
				"**When to use:** After a request message has been fully handled and the work is done. Distinct from ack (read) — resolved means action is complete.\n\n" +
				"**Output shape:** Confirmation of updated status.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id": map[string]any{"type": "string"},
					"agent_id":   map[string]any{"type": "string"},
					"message_id": map[string]any{"type": "string"},
				},
				"required": []string{"session_id", "agent_id", "message_id"},
			},
		},
		{
			Name: "nanite_message_catch_up",
			Description: "Get the last N messages for a session across both sides of the conversation.\n\n" +
				"**When to use:** At the start of a handoff or when an incoming agent needs a quick summary of recent session activity without reading the full transcript.\n\n" +
				"**Required context:** session_id is required. limit defaults to 20.\n\n" +
				"**Output shape:** Ordered list of the most recent messages (both sent and received) in the session.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id": map[string]any{"type": "string"},
					"limit":      map[string]any{"type": "integer", "default": 20},
				},
				"required": []string{"session_id"},
			},
		},
		{
			Name: "nanite_handoff_request",
			Description: "Request a session handoff, transferring primary responsibility from one agent to another.\n\n" +
				"**When to use:** When the current agent has completed its scope and another agent should take over. The handoff is pending until the user approves it via nanite_handoff_approve.\n\n" +
				"**Required context:** session_id, to_agent_id, and requested_by are required. requested_by identifies who initiated the handoff (departing agent, incoming agent, or user).\n\n" +
				"**Output shape:** Handoff record with ID. Chain with nanite_handoff_approve or nanite_handoff_reject.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id":    map[string]any{"type": "string"},
					"from_agent_id": map[string]any{"type": "string"},
					"to_agent_id":   map[string]any{"type": "string"},
					"requested_by":  map[string]any{"type": "string", "enum": []string{"departing", "incoming", "user"}},
				},
				"required": []string{"session_id", "to_agent_id", "requested_by"},
			},
		},
		{
			Name: "nanite_handoff_approve",
			Description: "Approve a pending handoff, atomically rebinding the session's primary agent.\n\n" +
				"**When to use:** After nanite_handoff_request, when the user (or logic) confirms the transfer should proceed.\n\n" +
				"**Required context:** handoff_id from a prior nanite_handoff_request call.\n\n" +
				"**Output shape:** Confirmation that the session is now bound to the new agent.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"handoff_id": map[string]any{"type": "string"},
				},
				"required": []string{"handoff_id"},
			},
		},
		{
			Name: "nanite_handoff_reject",
			Description: "Reject a pending handoff, cancelling the transfer request.\n\n" +
				"**When to use:** When the user or logic determines the handoff should not proceed. Provide a reason so the requesting agent can understand the outcome.\n\n" +
				"**Required context:** handoff_id from a prior nanite_handoff_request call.\n\n" +
				"**Output shape:** Confirmation that the handoff is cancelled; the session remains with the current agent.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"handoff_id": map[string]any{"type": "string"},
					"reason":     map[string]any{"type": "string"},
				},
				"required": []string{"handoff_id"},
			},
		},
		// --- Subagent spawn (S7 T9) ---
		{
			Name: "nanite_spawn_subagent",
			Description: "Spawn a child agent to handle a delegated subtask, optionally blocking until it completes.\n\n" +
				"**When to use:** When a subtask is well-bounded and can be handled by a specialized role without the primary agent doing the work inline. Use sync for tasks that the caller must wait for; async or api for fire-and-forget or parallel work.\n\n" +
				"**Mode semantics:**\n" +
				"- sync: blocks until the subagent returns; reply is in the tool result.\n" +
				"- async: returns immediately; reply lands in the parent session inbox (channel=inbox) when done.\n" +
				"- api: returns immediately; reply lands in the parent session chat (channel=chat).\n\n" +
				"**Required context:** parent_session_id, parent_agent_id, role, and prompt are required.\n\n" +
				"**Output shape (sync):** Subagent's final reply text. (async/api): run_id for tracking — use nanite_subagent_status to check progress.\n\n" +
				"**Chaining:** Follow async/api spawns with nanite_message_inbox to receive the reply, or nanite_subagent_status to check completion.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"parent_session_id": map[string]any{"type": "string", "description": "Session the spawning agent is in; the reply lands here."},
					"parent_agent_id":   map[string]any{"type": "string", "description": "Agent ID of the spawning (primary) agent."},
					"role":              map[string]any{"type": "string", "description": "Role slug (e.g. 'file-backend') the subagent is booted with."},
					"prompt":            map[string]any{"type": "string", "description": "Initial prompt for the subagent."},
					"mode":              map[string]any{"type": "string", "enum": []string{"sync", "async", "api"}, "description": "sync blocks; async returns immediately and replies via inbox; api returns immediately and replies via chat."},
					"inputs_json":       map[string]any{"type": "string", "description": "JSON blob of caller-specified inputs passed to the subagent."},
					"timeout_seconds":   map[string]any{"type": "integer", "description": "Wall-time cap for the subagent runner. 0 uses default (300)."},
					"provider":          map[string]any{"type": "string", "description": "Optional provider override for the child session (e.g. 'pty-claude', 'anthropic', 'openai'). Empty uses the agent profile's default provider. Enables budget-aware routing without modifying agent profiles."},
				},
				"required": []string{"parent_session_id", "parent_agent_id", "role", "prompt"},
			},
		},
		{
			Name: "nanite_subagent_status",
			Description: "Return the current lifecycle state of a spawned subagent run.\n\n" +
				"**When to use:** After nanite_spawn_subagent in async or api mode, to check whether the subagent has completed.\n\n" +
				"**Required context:** run_id from the nanite_spawn_subagent response.\n\n" +
				"**Output shape:** {run_id, status: pending|running|done|failed|cancelled, ...}.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id": map[string]any{"type": "string"},
				},
				"required": []string{"run_id"},
			},
		},
		{
			Name: "nanite_subagent_cancel",
			Description: "Cancel an in-flight subagent run. Idempotent — calling on an already-terminal run is a no-op.\n\n" +
				"**When to use:** When the parent agent no longer needs the subagent's result (e.g. user cancelled the request), or the subagent is taking too long.\n\n" +
				"**Required context:** run_id from the nanite_spawn_subagent response.\n\n" +
				"**Output shape:** Confirmation of cancellation or no-op if already terminal.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id": map[string]any{"type": "string"},
				},
				"required": []string{"run_id"},
			},
		},
		// --- Scratchpad tools (P4 Scratchpad, CW-20260419-0025) ---
		{
			Name: "nanite_scratchpad_write",
			Description: `Write a value to the per-turn scratchpad under a named key.

**When to use:** When you want to stash interim findings, running totals, or
partial results mid-turn so you can recall them later in the same generation
without re-deriving from the transcript. Use it when you'd otherwise re-run a
tool call to look up a value you already fetched, or when you need to accumulate
state across multiple tool calls.

**When NOT to use:** Do NOT use as long-term memory — that is Vanta
(nanite_memory_save / nanite_memory_recall). Do NOT use to pass data to another agent
— that is peer_query. Do NOT use across sessions — the scratchpad clears on
turn exit. This is NOT a replacement for the think tool: think is for reasoning
within one LLM call; scratchpad_write is for persisting a value you want to
retrieve later in the same turn.

**Output shape:** {"stored": true} on success. On a size violation, returns an
error string describing the byte count so you can decide to truncate or omit.`,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key": map[string]any{
						"type":        "string",
						"description": "Case-sensitive key (agent-chosen, e.g. 'summary', 'file_count', 'running_total')",
					},
					"value": map[string]any{
						"anyOf": []map[string]any{
							{"type": "string"},
							{"type": "number"},
							{"type": "integer"},
							{"type": "boolean"},
						},
						"description": "Scalar scratchpad value. For structured data, JSON-encode it into a string before writing. Max 8 KiB per value; 64 KiB total per turn.",
					},
				},
				"required": []string{"key", "value"},
			},
		},
		{
			Name: "nanite_scratchpad_read",
			Description: `Read values from the per-turn scratchpad.

**When to use:** After calling nanite_scratchpad_write earlier in this turn, to
retrieve what you stashed. Call with a specific key to read one entry, or omit
the key to read all current entries.

**When NOT to use:** Do not use to read data from previous turns or other
sessions — the scratchpad is cleared on turn exit and is per-generation only.
For cross-session data use Vanta (nanite_memory_recall). For data from other agents
use peer_query.

**Output shape:** {"entries": {key: value, ...}} — a map of matching entries.
Empty map when no entries match (key not found, or scratchpad is empty).`,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key": map[string]any{
						"type":        "string",
						"description": "Key to read. Omit to read all scratchpad entries.",
					},
				},
			},
		},
		{
			Name: "nanite_scratchpad_clear",
			Description: `Delete a key from the per-turn scratchpad.

**When to use:** When a stashed value is no longer needed mid-turn and you want
to free space for other writes (the turn total is capped at 64 KiB), or when
you want to reset a running total before recomputing it.

**When NOT to use:** Rarely needed — the entire scratchpad is automatically
cleared on turn exit. Only call this if you need the byte budget freed within
the current turn for subsequent writes.

**Output shape:** {"cleared": true} if the key existed and was removed;
{"cleared": false} if the key was not found (idempotent, not an error).`,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key": map[string]any{
						"type":        "string",
						"description": "Key to delete from the scratchpad.",
					},
				},
				"required": []string{"key"},
			},
		},
		// --- executeTask dispatch primitive (CW-20260421-0010, B3) ---
		{
			Name: "nanite_execute_task",
			Description: "Dispatch a task to a Worker or Planner role agent. The Chat agent (harness) calls this when the user's request needs concrete execution — file edits, tool runs, code work, planning — instead of a direct conversational reply.\n\n" +
				"**When to use:** When the user asks for any work that requires tool calls beyond Chat's static surface (todos / plans / scratchpad / messaging / narration / executeTask itself). Examples: \"fix the bug\", \"audit X\", \"refactor Y\", \"build Z\".\n\n" +
				"**When NOT to use:** Trivial conversational replies (\"thanks\", \"what does X mean\"). The Chat harness handles those directly without dispatch.\n\n" +
				"**Behavior:** ScopeTier classifies the request, selects a Role (Worker for execution, Planner for open-scope breakdown), spawns the role agent with its own task-appropriate tool surface, captures the result, and returns it as a structured envelope. The Chat agent's context never sees raw worker output — only the envelope.\n\n" +
				"**Required context:** session_id (your current session) and message (the task to dispatch). parent_agent_id, provider, and timeout_seconds are optional overrides.\n\n" +
				"**Output shape:** A structured envelope JSON the harness relays to the user. The envelope's `type` describes the result shape (report-card, document-viewer, etc.).\n\n" +
				"**Static surface note:** This is the ONLY way the Chat harness dispatches work. Do not expect raw spawn / shell / file tools — those are not on Chat's surface.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id":      map[string]any{"type": "string", "description": "The current session ID. The dispatched role's reply lands here."},
					"parent_agent_id": map[string]any{"type": "string", "description": "Chat agent ID making the dispatch. Optional; defaults are inferred."},
					"message":         map[string]any{"type": "string", "description": "The task or request to hand off. Pass through the user's original phrasing when possible — ScopeTier classifies on this string."},
					"provider":        map[string]any{"type": "string", "description": "Optional provider override for the spawned role's session. Empty uses the role profile's default."},
					"timeout_seconds": map[string]any{"type": "integer", "description": "Wall-time cap for the spawned role. 0 uses default (300)."},
				},
				"required": []string{"session_id", "message"},
			},
		},
	}
}
