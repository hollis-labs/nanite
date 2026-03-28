package mcp

import "github.com/hollis-labs/conduit/internal/provider"

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
			Name:        "conduit_create_skill",
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
			Name:        "conduit_list_skills",
			Description: "List all skills. Returns name, slug, category, and description for each.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"category": map[string]any{"type": "string", "description": "Filter by category (optional)"},
				},
			},
		},
		{
			Name:        "conduit_update_skill",
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
			Name:        "conduit_delete_skill",
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
			Name:        "conduit_create_agent",
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
			Name:        "conduit_list_agents",
			Description: "List all agent profiles. Returns name, slug, description, and default model.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "conduit_update_agent",
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
		// UI trigger tools — open frontend modals/panels
		{
			Name:        "conduit_open_sprint_planning",
			Description: "Open the sprint planning modal in the UI. Use when the user asks to review sprints, plan work, or manage tasks and backlog.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project_id": map[string]any{"type": "string", "description": "Optional project ID to scope the view (omit for all projects)"},
				},
			},
		},
		// Cross-app navigation tools — control Engine GUI via SSE
		{
			Name:        "conduit_navigate_engine",
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
						"description": "Entity ID for detail pages (e.g. TASK-123 for task-detail, SPR-456 for sprint-detail). Optional.",
					},
					"project_id": map[string]any{
						"type":        "string",
						"description": "Filter by project ID (e.g. 'conduit', 'engine', 'cortex'). Optional.",
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
			Name:        "conduit_refresh_engine",
			Description: "Trigger a data refresh in the Engine GUI. Use after making changes (task transitions, sprint updates) so the GUI reflects the latest state.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "conduit_show_giphy",
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
			Name:        "conduit_run_report",
			Description: "Run a background report and notify the user when it's done. The report generates asynchronously; the user will see a notification card when complete and can click to view the full output.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"report_type": map[string]any{
						"type":        "string",
						"description": "Type of report: executive-summary, sprint-progress, portfolio-health",
					},
					"description": map[string]any{
						"type":        "string",
						"description": "Brief description shown while report generates",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "Pre-baked report content (HTML or markdown). If provided, used directly instead of calling Hadron. Enables demo mode.",
					},
				},
				"required": []string{"report_type"},
			},
		},
		{
			Name:        "conduit_show_document",
			Description: "Display a document in chat as a rich scrollable viewer. Use for executive summaries, reports, meeting notes, or any long-form content the user should read.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":             map[string]any{"type": "string", "description": "Document title"},
					"content":           map[string]any{"type": "string", "description": "Document body (HTML or markdown)"},
					"format":            map[string]any{"type": "string", "description": "Content format: html or markdown (default: markdown)"},
					"sections":          map[string]any{"type": "string", "description": "Comma-separated section names for jump-nav (optional)"},
					"download_filename": map[string]any{"type": "string", "description": "Filename for download button (optional, e.g. report.html)"},
				},
				"required": []string{"title", "content"},
			},
		},
		{
			Name:        "conduit_show_report",
			Description: "Display a metrics report card in chat with labeled values, progress bars, and action buttons. Use for sprint progress, portfolio health, or status summaries.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":   map[string]any{"type": "string", "description": "Report title"},
					"metrics": map[string]any{"type": "string", "description": "JSON array of metric objects: [{label, value, percent?, color?}]. Colors: emerald, green, amber, red, blue, violet."},
					"summary": map[string]any{"type": "string", "description": "Summary text (markdown). Optional."},
					"actions": map[string]any{"type": "string", "description": "JSON array of action objects: [{label, action, id?}]. Optional."},
				},
				"required": []string{"title", "metrics"},
			},
		},
		{
			Name:        "conduit_show_task_disposition",
			Description: "Display an interactive task triage card in chat. Users can set a disposition (Approve, Archive, Pause, Done, Skip) for each task via dropdowns. On submit, a structured message is sent back for you to process.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":       map[string]any{"type": "string", "description": "Card title (e.g. 'Sprint Triage — SPR-DEMO')"},
					"description": map[string]any{"type": "string", "description": "Description text shown above tasks. Optional."},
					"tasks":       map[string]any{"type": "string", "description": "JSON array of task objects: [{id, title, status, priority}]"},
					"actions":     map[string]any{"type": "string", "description": "JSON array of action strings. Default: [\"Approve\", \"Archive\", \"Pause\", \"Done\", \"Skip\"]"},
				},
				"required": []string{"title", "tasks"},
			},
		},
		{
			Name:        "conduit_show_sprint_planning_review",
			Description: "Display an interactive sprint planning review card. Shows tasks with suggested sprint assignments. Users can accept or move tasks to different sprints. Each action is reactive — updates Engine in real-time. Use after creating demo sprints and tasks, when the user wants to review and assign them.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":       map[string]any{"type": "string", "description": "Card title (e.g. 'Sprint Planning Review')"},
					"description": map[string]any{"type": "string", "description": "Description text. Optional."},
					"sprints":     map[string]any{"type": "string", "description": "JSON array of sprint objects: [{id, name}]"},
					"tasks":       map[string]any{"type": "string", "description": "JSON array of task objects: [{id, title, summary, suggested_sprint, priority, status}]. The suggested_sprint should be a sprint ID from the sprints array."},
					"page_size":   map[string]any{"type": "number", "description": "Tasks per page (default: 10)"},
				},
				"required": []string{"title", "sprints", "tasks"},
			},
		},
		// Builder tools — interactive step-by-step creation flows
		{
			Name:        "conduit_start_builder",
			Description: "Start a step-by-step creation wizard for agents, skills, or prompt templates. NOT for asking arbitrary questions — only for creating new entities. You MUST provide builder_name (agent, skill, or prompt_template). After starting, use conduit_builder_step for each subsequent step.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"builder_name": map[string]any{"type": "string", "description": "Builder to start: agent, skill, or prompt_template. Omit to list available builders."},
				},
			},
		},
		{
			Name:        "conduit_builder_step",
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
	}
}
