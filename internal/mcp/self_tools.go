package mcp

import "github.com/hollis-labs/go-providers/provider"

// showEnvelopeTargetDesc and showEnvelopeModeDesc are shared input-schema
// descriptions for the optional `target` and `mode` fields on
// nanite_show_card. They mirror the chat.Envelope.Target / chat.Envelope.Mode
// contract (J8 v1 — CW-20260426-0006); the FE picks them up via
// applyEnvelopePanelEffects.
const (
	showEnvelopeTargetDesc = "Optional drawer ID to OPEN when this card arrives. v1 vocabulary: " +
		"\"bottom_chat_drawer\" (long-form reference content below the chat transcript), " +
		"\"work\" (right-rail todos/plans/sprint cards), \"workflows\" (right-rail guided-interaction templates). " +
		"Plugin-declared drawers may add more. Visibility hint only — does NOT control where the card renders. " +
		"For routing the card itself, use `render_target` instead."
	showEnvelopeRenderTargetDesc = "Optional panel ID where the card SHOULD RENDER (A2 — CW-20260428-0008). " +
		"v1 vocabulary: \"bottom_chat_drawer\", \"work\", \"workflows\" (built-in panels are always allowed); " +
		"plugin-declared panels are accepted only when the calling agent has H1 trust — otherwise the routing is " +
		"dropped and the card falls back to inline with a `render_target_blocked` hint. " +
		"When omitted, the schema's `default_render_target` (if any) is used; for the 10 passive renderables that's " +
		"\"bottom_chat_drawer\". Pass an empty string to force-inline a card whose schema would otherwise route to a drawer."
	showEnvelopeModeDesc = "Optional workspace mode hint that travels with the envelope. " +
		"v1 vocabulary: \"planning\" (FE preset opens [work, workflows]). " +
		"Independent of `target` and `render_target` — all three can be set. Empty/unknown values are silent no-ops on the FE."
)

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
// NOTE: nanite_run_python is included here so it appears in the worker/planner
// surface, but it is intentionally NOT added to dispatch.ChatToolSurface and
// does not match any prefix in ChatToolSurface — EnforceChatSurface will
// filter it out for Chat agents. (CW-20260420-0019, D6)
func selfToolDefinitions() []Tool {
	return []Tool{
		naniteRunPythonToolDefinition(),
		validateToolDefinition(),
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
			Name: "nanite_giphy_search",
			Description: "Search GIPHY for an animated GIF and return its URL plus metadata. Pure data fetch — does NOT render anything in chat.\n\n" +
				"**When to use:** When the user asks for a GIF or the conversation tone calls for a visual reaction. Call this first to fetch the URL, then chain into nanite_show_card to render.\n\n" +
				"**Chaining pattern:**\n" +
				"1. nanite_giphy_search(query: \"celebration\") → {gif_url, title, attribution, alt_text}\n" +
				"2. nanite_show_card(type: \"giphy-modal\", data: {gif_url: <step 1>, title: <step 1 title>, source: <step 1 attribution>, query: \"celebration\"})\n\n" +
				"**Demo mode:** When the server's GIPHY_API_KEY is unset, this returns one of a curated set of demo GIFs (deterministic per query). Useful for development; production should set the key.\n\n" +
				"**Output shape:** Single object {gif_url, title, attribution, alt_text} for limit=1 (default). For limit>1: {results: [{...}, ...]}. " +
				"Failures return a structured-error object instead: {error: \"http_error\"|\"no_results\"|\"parse_error\", details?, query}. IsError stays false on these — the call succeeded, the search did not. Branch on `error`.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "Search term (e.g. 'celebration', 'thumbs up', 'mind blown')"},
					"limit": map[string]any{"type": "number", "description": "Max results (default 1, max 10). Limit=1 returns a single object; limit>1 returns {results: [...]}."},
				},
				"required": []string{"query"},
			},
		},
		{
			Name: "nanite_show_card",
			Description: "Render a structured envelope card in chat or in a drawer. One generic surface for the v1 passive-renderable card types — replaces the older per-type nanite_show_giphy / nanite_show_document / nanite_show_report tools (CW-20260428-0019, A3).\n\n" +
				"**When to use:** When you want to display structured content (a metric, a list, a table, a side-by-side diff, a long-form document, a metrics report, an animated GIF). Pick the smallest card that fits the data.\n\n" +
				"**When NOT to use:** Decision-flow envelopes (approval-card, proposal-card, confirmation-card, question-form), runtime-emitted envelopes (chat-loop-terminated, elicitation-prompt), and plugin-shipped envelopes (kb-result, ticket-*) have their own emission paths and are NOT addressable here.\n\n" +
				"**Required context:** `type` from the v1 allow-list and `data` matching the per-type schema. The handler validates `data` against `internal/envelope/schemas/<type>.schema.json` at the boundary; payloads that miss required fields, wrong types, or carry unknown keys are rejected with a structured error citing the schema field that failed.\n\n" +
				"**Grounding:** For prose-bearing card types (`report-card`, `document-viewer`) you MUST also pass `sources` — a JSON array of `{tool_use_id, tool_name, note?}` objects citing the tool calls whose results ground the content. If you didn't fetch the data this turn, render a plain-text reply instead of an empty card.\n\n" +
				"**Render destination:** By default the card lands wherever the schema's `default_render_target` says — for the 10 passive renderables that's the bottom drawer. Pass `render_target` to override, or `render_target=\"\"` to force-inline. `target` (visibility — open this drawer) and `mode` (workspace preset) remain independent levers; both can travel with the envelope.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type": map[string]any{
						"type":        "string",
						"description": "Envelope type from the v1 passive-renderable allow-list. Each value validates against its own per-type schema.",
						"enum": []string{
							"giphy-modal",
							"document-viewer",
							"report-card",
							"info-card",
							"list-card",
							"metric-card",
							"progress-card",
							"table-card",
							"timeline-card",
							"diff-card",
						},
					},
					"data": map[string]any{
						"type":        "object",
						"description": "Card payload matching the schema for the chosen `type`. Schemas live at internal/envelope/schemas/<type>.schema.json — fields the schema doesn't declare are rejected (additionalProperties:false).",
					},
					"sources": map[string]any{
						"type":        "string",
						"description": "JSON array of grounding objects: [{tool_use_id, tool_name, note?}]. REQUIRED for report-card and document-viewer. Each source must be a tool_use_id from a tool call in THIS generation whose result materially informs the content. If you didn't fetch the data, don't render the card.",
					},
					"target":        map[string]any{"type": "string", "description": showEnvelopeTargetDesc},
					"render_target": map[string]any{"type": "string", "description": showEnvelopeRenderTargetDesc},
					"mode":          map[string]any{"type": "string", "description": showEnvelopeModeDesc},
				},
				"required": []string{"type", "data"},
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
			Description: "Create a todo item scoped to project, session, or turn (D1, CW-20260428-0014).\n\n" +
				"**When to use:** When the user asks to track a task, action item, or follow-up. Choose the scope that matches where the work lives:\n" +
				"- `session` (default): items tied to this chat conversation only. scope_id auto-filled from the current session when omitted.\n" +
				"- `project`: items that belong to a project and surface in any session of the same project. scope_id = project_id (or pass project_id explicitly).\n" +
				"- `turn`: items scoped to the current turn (rare — most callers want session).\n\n" +
				"**When NOT to use:** Do not use for multi-step plans with dependencies — use nanite_plan_create for those.\n\n" +
				"**Required context:** `scope=project` requires `project_id`. For scope=session/turn, `scope_id` is auto-filled from the current session context if omitted.\n\n" +
				"**Output shape:** \"Created todo <title> (<id>)\" on success.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":       map[string]any{"type": "string", "description": "Todo title"},
					"scope":       map[string]any{"type": "string", "enum": []string{"turn", "session", "project"}, "description": "Scope: turn, session, or project (default: session)"},
					"scope_id":    map[string]any{"type": "string", "description": "Scope ID (session_id for turn/session; project_id for project). Auto-filled from the current chat session when scope is 'session'/'turn' and this field is omitted."},
					"project_id":  map[string]any{"type": "string", "description": "Project ID — required when scope=project. Resolved from the current session's project when omitted."},
					"priority":    map[string]any{"type": "string", "description": "Priority: low, medium, high, critical (default: medium)"},
					"description": map[string]any{"type": "string", "description": "Detailed description (optional)"},
					"parent_id":   map[string]any{"type": "string", "description": "Parent todo ID for nesting (optional)"},
					"labels":      map[string]any{"type": "string", "description": "JSON array of label strings (optional)"},
				},
				"required": []string{"title"},
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
				"**Scope semantics:** Pass `scope` + `scope_id` to get a correctly scoped live card. For `scope=session`, `scope_id` is auto-filled from the current session context when omitted — you do not need to supply it explicitly. For `scope=project`, supply the project_id explicitly (or rely on the current session's project).\n\n" +
				"**Output shape:** Text summary of matching todos (count + titles). When `scope` is provided, also emits an interactive todo-list envelope that the UI renders as a live card (lazy-fetches current data at render time — NOT the snapshot from this call). Do NOT emit a nanite-envelope block manually — this tool handles that automatically.\n\n" +
				"**When NOT to use:** Do not call without `scope` if you want the interactive card — a scopeless call returns text only and emits no card.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scope":    map[string]any{"type": "string", "enum": []string{"turn", "session", "project"}, "description": "Filter by scope: turn, session, or project. Required to render an interactive card."},
					"scope_id": map[string]any{"type": "string", "description": "Scope ID (session_id for turn/session; project_id for project). Auto-filled from the current chat session when scope is 'session' and this field is omitted."},
					"project_id": map[string]any{"type": "string", "description": "Convenience: filter by project_id directly (matches both project-scoped todos and session/turn todos whose originating session belongs to this project)."},
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
		// --- Background job (P9 BackgroundJob, CW-20260420-0016) ---
		{
			Name: "nanite_background_job",
			Description: "Dispatch a long-running task as an async, non-session-bound background job. Returns a job_id immediately; the result lands as an inbox notification on the originating session when the job completes.\n\n" +
				"**Contrast with sibling primitives — pick the right one:**\n" +
				"- **peer_query** (sync, in-session): a single quick question to a peer agent; you wait for the reply inline. Lightest.\n" +
				"- **nanite_spawn_subagent** (sync/async, in-session): a delegated subtask whose lifecycle is tied to this session. Medium.\n" +
				"- **nanite_background_job** (async, NON-session-bound): heavy or long-running work that should not block this session. Reply arrives via the messaging inbox when done — possibly after this turn ends.\n\n" +
				"**When to use:** Codebase crawls, multi-file research, transcript analysis, or anything you'd otherwise abandon partway through because the calling turn ends. Only fires when the P3 ScopeTier classifier says ExecutionPattern=background — pass that pattern explicitly.\n\n" +
				"**Required context:** task (the prompt the spawned process runs), originating_session_id, originating_agent_id. agent (role slug) and budget are optional (defaults: 30 min wall-clock, 1 MiB output cap).\n\n" +
				"**Output shape:** {job_id}. The completion envelope (channel=inbox, kind=notification, from_agent_id=background-job) carries a structured JobResult JSON in payload_json with status (succeeded/failed/cancelled), output, error, started_at, completed_at. Poll nanite_message_inbox after the turn or chain via nanite_background_status.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task":                   map[string]any{"type": "string", "description": "The prompt / instruction the background process executes."},
					"agent":                  map[string]any{"type": "string", "description": "Optional agent slug for the dispatched job. Backend-defined."},
					"originating_session_id": map[string]any{"type": "string", "description": "Session that submitted the job; the completion envelope is delivered here."},
					"originating_agent_id":   map[string]any{"type": "string", "description": "Agent that submitted the job; conventionally the to_agent on the completion envelope."},
					"wall_clock_seconds":     map[string]any{"type": "integer", "description": "Optional wall-clock cap. 0 uses default (1800s)."},
					"max_output_bytes":       map[string]any{"type": "integer", "description": "Optional captured-output cap. 0 uses default (1 MiB)."},
				},
				"required": []string{"task", "originating_session_id", "originating_agent_id"},
			},
		},
		{
			Name: "nanite_background_status",
			Description: "Return the current lifecycle state and (when terminal) result of a background job.\n\n" +
				"**When to use:** After nanite_background_job, to poll progress. Prefer waiting for the inbox notification; this is for active polling cases.\n\n" +
				"**Required context:** job_id from the nanite_background_job response.\n\n" +
				"**Output shape:** {job_id, status: pending|running|succeeded|failed|cancelled, output, error, started_at, completed_at, output_truncated}.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"job_id": map[string]any{"type": "string"},
				},
				"required": []string{"job_id"},
			},
		},
		{
			Name: "nanite_background_cancel",
			Description: "Cancel an in-flight background job. Idempotent — calling on an already-terminal job is a no-op.\n\n" +
				"**When to use:** When the background work is no longer needed (e.g. the user cancelled the request, or a faster path materialized). The backend kills the process group; descendants are reaped.\n\n" +
				"**Required context:** job_id from the nanite_background_job response.\n\n" +
				"**Output shape:** Confirmation of cancellation or no-op if already terminal.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"job_id": map[string]any{"type": "string"},
				},
				"required": []string{"job_id"},
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
		// --- chat_search self-tool (P8B, CW-20260420-0026) ---
		{
			Name: "nanite_chat_search",
			Description: "Search your own past conversation, including parts that were summarized away during compaction. " +
				"Use this when the post-compaction disclosure prompt mentions an elided detail you need.\n\n" +
				"**When to use:** When you need to find something from earlier in this session — a specific decision, " +
				"a value, a file path, or any detail the user mentioned — including content that may have been " +
				"compacted into a summary.\n\n" +
				"**Anti-pattern:** Do NOT use for general knowledge questions or external research — this only searches " +
				"THIS session's history. Use Vanta or web search for those.\n\n" +
				"**Output shape:** Returns up to N snippets, each with: turn_id, role (user/assistant), excerpt with " +
				"query highlighted, source (active|summary), compaction_event_id (if from a summary).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Substring or regex to find (case-insensitive)",
					},
					"scope": map[string]any{
						"type":        "string",
						"enum":        []string{"active", "compacted", "all"},
						"description": "Which messages to search: active (not summarized), compacted (summary blobs only), or all (default: all)",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max snippets to return (default 20, max 100)",
					},
				},
				"required": []string{"query"},
			},
		},
		// --- Panel control (J8 v1, CW-20260426-0006) ---
		{
			Name: "nanite_panel_open",
			Description: "Open a UI drawer/panel by stable ID so its content is visible to the user.\n\n" +
				"**When to use:** When the conversation enters a state where a particular surface is helpful — e.g. asking the user to triage todos opens `work`, picking a workflow opens `workflows`, surfacing a long document for reference opens `bottom_chat_drawer`.\n\n" +
				"**When NOT to use:** Do NOT call this just because content is being emitted into a drawer — envelopes can carry a `target` field that auto-routes and opens the drawer declaratively. Use the explicit tool only when you want to bring a drawer up WITHOUT (or before) emitting envelope content.\n\n" +
				"**v1 catalog:** `bottom_chat_drawer`, `work`, `workflows`. Plugin-shipped panel IDs are also accepted but require `trusted` H1 tier on the calling agent profile; untrusted callers receive an `untrusted` error.\n\n" +
				"**Dismiss policy:** if the user has dismissed this panel since the last conversational trigger, this call is a NO-OP — the agent does not fight the user for visibility. The tool returns `{opened: true|false, reason}` so the agent can verify.\n\n" +
				"**Output shape:** `{opened: bool, panel_id: string, reason?: string}` — `reason` is set when `opened=false` (`user_dismissed`, `unknown_panel`, `untrusted`).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"panel_id": map[string]any{
						"type":        "string",
						"description": "Stable panel ID. v1 built-ins: bottom_chat_drawer, work, workflows. Plugin panel IDs accepted from trusted callers.",
					},
				},
				"required": []string{"panel_id"},
			},
		},
		{
			Name: "nanite_panel_close",
			Description: "Close a UI drawer/panel by stable ID.\n\n" +
				"**When to use:** Sparingly — closing surfaces hides information from the user. Reasonable case: a workflow finished and you want to clear the workflow drawer so the user's attention returns to chat.\n\n" +
				"**When NOT to use:** Do not close a drawer the user opened manually (`user_opened` state). The dispatcher enforces this: a close on a user-opened drawer is a NO-OP.\n\n" +
				"**v1 catalog:** `bottom_chat_drawer`, `work`, `workflows`. Plugin-shipped panel IDs are accepted but require `trusted` H1 tier.\n\n" +
				"**Output shape:** `{closed: bool, panel_id: string, reason?: string}`. `reason` is `user_opened`, `unknown_panel`, or `untrusted` on no-ops.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"panel_id": map[string]any{
						"type":        "string",
						"description": "Stable panel ID to close. v1 built-ins: bottom_chat_drawer, work, workflows.",
					},
				},
				"required": []string{"panel_id"},
			},
		},
		{
			Name: "nanite_signal_mode",
			Description: "Signal a workspace mode/status to the FE so it can open the corresponding preset of panels.\n\n" +
				"**When to use:** When the conversation enters a recognized state where multiple drawers should come up together — e.g. switching into planning surfaces both `work` and `workflows`. Cleaner than calling `nanite_panel_open` per drawer.\n\n" +
				"**v1 vocabulary:** `planning` → opens [work, workflows]. The map is intentionally tiny in v1; emitting an unknown mode is a NO-OP on the FE (no error). The vocabulary is documented at ui/src/lib/panel-modes.ts.\n\n" +
				"**Dismiss policy:** the same 4-state dismiss machine that gates `nanite_panel_open` applies — if the user has dismissed `work`, the planning preset will NOT re-open it until a new conversational trigger fires.\n\n" +
				"**Output shape:** `{signaled: true, mode: string}` on success. Unknown modes still return `signaled: true` (the FE silently ignores them) — the contract is that emission always succeeds; preset interpretation lives in the FE.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"mode": map[string]any{
						"type":        "string",
						"description": "Mode name. v1: planning. Map is FE-defined and extensible.",
					},
				},
				"required": []string{"mode"},
			},
		},
		// --- Reminders + Pin (J11, CW-20260426-0009; D1, CW-20260428-0014) ---
		{
			Name: "nanite_set_reminder",
			Description: "Set a deterministic reminder that fires at a future time or after N turns, " +
				"injecting your reminder text into context as a <system-reminder> block.\n\n" +
				"**When to use:** When you want to remember to do something later — e.g. 'don't forget to file a ticket', " +
				"'review the plan after 5 turns', 'check status at 3pm'.\n\n" +
				"**Trigger shapes (v1):**\n" +
				"- Time-based: `{\"type\":\"time\",\"at\":\"<RFC3339>\"}` — fires when the clock reaches the given time.\n" +
				"- Turn-count: `{\"type\":\"turn_count\",\"n\":5}` — fires N turns after this call.\n\n" +
				"**Scope (D1, CW-20260428-0014):**\n" +
				"- `turn`: fires within the same turn it was created in.\n" +
				"- `session` (default): fires only in the originating session.\n" +
				"- `project`: fires in any session of the same project; requires project_id (resolved from the current session's project when omitted).\n\n" +
				"**When NOT to use:** Do not use for calendar events, cross-system notifications, or anything requiring " +
				"an LLM to decide when to fire — triggers are always deterministic in v1.\n\n" +
				"**Reminder display:** When a reminder fires, its text is injected as `<system-reminder>` into the next turn's " +
				"context and surfaced in the I1 dev-mode inspector. There is no UI toast in v1.\n\n" +
				"**Output shape:** `{reminder_id, scope, status: 'set', trigger}`.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{
						"type":        "string",
						"description": "The reminder text to inject when the trigger fires.",
					},
					"trigger": map[string]any{
						"type":        "object",
						"description": "Trigger condition. v1 shapes: {type:'time',at:'<RFC3339>'} or {type:'turn_count',n:<N>}.",
						"properties": map[string]any{
							"type": map[string]any{"type": "string", "enum": []string{"time", "turn_count"}},
							"at":   map[string]any{"type": "string", "description": "RFC3339 fire time (for type=time)"},
							"n":    map[string]any{"type": "integer", "description": "Number of turns to wait (for type=turn_count)"},
						},
						"required": []string{"type"},
					},
					"scope": map[string]any{
						"type":        "string",
						"enum":        []string{"turn", "session", "project"},
						"description": "Reminder lifetime. Default: session.",
					},
					"project_id": map[string]any{
						"type":        "string",
						"description": "Project ID — required when scope=project. Auto-resolved from the current session's project when omitted.",
					},
				},
				"required": []string{"text", "trigger"},
			},
		},
		{
			Name: "nanite_pin",
			Description: "Pin content so the system keeps it in context across turns (session scope) or across sessions in a project (project scope). " +
				"Pinned content rides in the SlotUserContext budget and is visible in the bottom drawer Pins tab.\n\n" +
				"**When to use:** When you want to keep a piece of context visible throughout the conversation or across sessions — " +
				"e.g. a key decision, a reference snippet, a current task description.\n\n" +
				"**Scopes (D1, CW-20260428-0014):**\n" +
				"- `turn`: ephemeral, cleared after the current turn (not stored in DB).\n" +
				"- `session` (default): survives compaction, cleared at session end.\n" +
				"- `project`: persists for the project; surfaces in any session of the same project. Requires project_id (resolved from the current session's project when omitted).\n\n" +
				"**Budget:** Pinned content shares the 2000-token SlotUserContext budget. " +
				"Oldest pins truncate first when over budget. Keep pins thin.\n\n" +
				"**Output shape:** `{pin_id, scope, status: 'pinned'}`.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"content": map[string]any{
						"type":        "string",
						"description": "Content to pin. Keep thin — shares the 2000-token SlotUserContext budget.",
					},
					"scope": map[string]any{
						"type":        "string",
						"enum":        []string{"turn", "session", "project"},
						"description": "Pin lifetime. Default: session.",
					},
					"project_id": map[string]any{
						"type":        "string",
						"description": "Project ID — required when scope=project. Auto-resolved from the current session's project when omitted.",
					},
				},
				"required": []string{"content"},
			},
		},
		{
			Name: "nanite_unpin",
			Description: "Remove a pinned item by ID, freeing its context budget.\n\n" +
				"**When to use:** When pinned content is no longer needed — after the user acknowledges it, " +
				"after the task it describes is complete, or when the budget needs freeing.\n\n" +
				"**Required context:** pin_id from a prior nanite_pin call.\n\n" +
				"**Output shape:** `{pin_id, status: 'unpinned'}`.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pin_id": map[string]any{
						"type":        "string",
						"description": "Pin ID to remove (from prior nanite_pin call).",
					},
				},
				"required": []string{"pin_id"},
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
