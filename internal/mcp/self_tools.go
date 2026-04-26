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
			Name:        "nanite_create_skill",
			Description: "Create a new skill. Skills bind tool names to a category and description.",
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
			Name:        "nanite_list_skills",
			Description: "List all skills. Returns name, slug, category, and description for each.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"category": map[string]any{"type": "string", "description": "Filter by category (optional)"},
				},
			},
		},
		{
			Name:        "nanite_update_skill",
			Description: "Update an existing skill by ID. Only provided fields are changed.",
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
			Name:        "nanite_delete_skill",
			Description: "Delete a skill by ID. Only non-builtin skills can be deleted.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "Skill ID to delete"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "nanite_create_agent",
			Description: "Create a new agent profile with a name, slug, system prompt, and optional default model.",
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
			Name:        "nanite_list_agents",
			Description: "List all agent profiles. Returns name, slug, description, and default model.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "nanite_update_agent",
			Description: "Update an existing agent profile by ID. Only provided fields are changed.",
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
			Name:        "nanite_show_giphy",
			Description: "Search Giphy for an animated GIF and display it in chat as a rich card. Use for fun, celebration, or to lighten the mood.",
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
			Name:        "nanite_start_builder",
			Description: "Start a step-by-step creation wizard for agents, skills, or prompt templates. NOT for asking arbitrary questions — only for creating new entities. You MUST provide builder_name (agent, skill, or prompt_template). After starting, use nanite_builder_step for each subsequent step.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"builder_name": map[string]any{"type": "string", "description": "Builder to start: agent, skill, or prompt_template. Omit to list available builders."},
				},
			},
		},
		{
			Name:        "nanite_builder_step",
			Description: "Submit a value for the current step in an active builder flow. Returns the next step prompt or the final result.",
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
			Name:        "nanite_todo_create",
			Description: "Create a todo item. Todos are scoped to workspace, project, or session. Use for tracking work items, action items, and tasks.",
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
			Name:        "nanite_todo_update",
			Description: "Update a todo's status, priority, title, or description. Use to mark items done, change priority, or update details.",
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
			Name:        "nanite_todo_list",
			Description: "List todos with optional filters. Returns a text summary plus an interactive todo-list card (when scope is provided) that the UI renders with live data. Pass scope and scope_id so the card is correctly scoped to the current session or project. Do NOT emit a nanite-envelope block manually — this tool already does that.",
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
			Name:        "nanite_plan_create",
			Description: "Create a plan with ordered steps. Plans organize work into phases with dependencies and acceptance criteria. After creating a plan with status 'proposed', emit a plan-review envelope so the user can approve/reject it inline: ```nanite-envelope\n{\"kind\":\"envelope\",\"version\":1,\"type\":\"plan-review\",\"data\":{\"plan_id\":\"...\",\"title\":\"...\",\"description\":\"...\",\"status\":\"proposed\",\"steps\":[{\"id\":\"...\",\"title\":\"...\"}]}}\n```",
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
			Name:        "nanite_plan_update",
			Description: "Update a plan or a specific step within a plan. Can update plan-level fields or transition a single step's status.",
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
			Name:        "nanite_plan_list",
			Description: "List plans with optional filters. Returns plans matching the given scope, scope_id, and/or status.",
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
			Name:        "nanite_plan_get",
			Description: "Get a single plan by ID. Returns the full plan as JSON, including steps.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "Plan ID"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "nanite_plan_delete",
			Description: "Delete a plan by ID. Irreversible.",
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
			Name:        "nanite_install_home",
			Description: "Extract embedded Nanite framework assets to ~/.nanite/. Skips user-modified files unless force=true.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"force": map[string]any{"type": "boolean", "description": "overwrite user-modified files"},
				},
			},
		},
		{
			Name:        "nanite_install_project",
			Description: "Scaffold .nanite/ and NANITE.md in a project. Optionally migrate from .agentrc/ or archive-only.",
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
			Name:        "nanite_install_rollback",
			Description: "Reverse the most recent migration for a project from its archive snapshot.",
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
			Name:        "nanite_install_diff",
			Description: "Dry-run a project install: show what would change without modifying anything. (Not yet implemented.)",
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
			Name:        "nanite_message_send",
			Description: "Send a message addressed to (to_session_id, to_agent_id). Use 'user' for to_agent_id to reach the human in a session. Set reply_to to the parent message ID to continue an existing thread. Channel policy: 'chat' for in-session conversation, 'inbox' for async polled work, 'alert' for agent-triggered one-off notifications.",
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
			Name:        "nanite_message_inbox",
			Description: "Read the messaging inbox for (session_id, agent_id). Optional status and channel filters.",
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
			Name:        "nanite_message_thread",
			Description: "Get all messages in a thread by thread_id. Thread is participant-filtered by (session_id, agent_id) — only messages where the caller is sender or recipient come back. Non-participants see an empty slice (no existence leak).",
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
			Name:        "nanite_message_ack",
			Description: "Mark a message as read.",
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
			Name:        "nanite_message_resolve",
			Description: "Mark a message as resolved.",
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
			Name:        "nanite_message_catch_up",
			Description: "Get the last N messages for a session across both sides of the conversation. Used for handoff catch-up.",
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
			Name:        "nanite_handoff_request",
			Description: "Request a session handoff from one agent to another. Creates a pending row; user must approve.",
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
			Name:        "nanite_handoff_approve",
			Description: "Approve a pending handoff. Atomically rebinds the session's primary agent and marks the handoff complete.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"handoff_id": map[string]any{"type": "string"},
				},
				"required": []string{"handoff_id"},
			},
		},
		{
			Name:        "nanite_handoff_reject",
			Description: "Reject a pending handoff with a reason.",
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
			Name:        "nanite_spawn_subagent",
			Description: "Spawn an inline subagent to handle a subtask. Sync mode blocks until the subagent returns; async/api modes return immediately and the subagent's reply lands in the parent session (inbox channel for async, chat channel for api). On completion a message of kind=reply is delivered back to parent_agent_id.",
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
			Name:        "nanite_subagent_status",
			Description: "Return the current lifecycle state of a spawned subagent run.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id": map[string]any{"type": "string"},
				},
				"required": []string{"run_id"},
			},
		},
		{
			Name:        "nanite_subagent_cancel",
			Description: "Cancel an in-flight subagent run. Idempotent; already-terminal runs are no-ops.",
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
	}
}
