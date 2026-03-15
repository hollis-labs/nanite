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
		{
			Name:        "conduit_list_workflows",
			Description: "List all workflows. Returns name, slug, trigger, and enabled status.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "conduit_create_workflow",
			Description: "Create a new workflow with a name, slug, trigger type, and JSON/YAML definition.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":       map[string]any{"type": "string", "description": "Workflow display name"},
					"slug":       map[string]any{"type": "string", "description": "URL-safe slug (unique)"},
					"trigger":    map[string]any{"type": "string", "description": "Trigger type: manual, on_message, scheduled (default: manual)"},
					"definition": map[string]any{"type": "string", "description": "Workflow definition as JSON string"},
					"is_enabled": map[string]any{"type": "boolean", "description": "Whether the workflow is enabled (default: true)"},
				},
				"required": []string{"name", "slug", "definition"},
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
		// Builder tools — interactive step-by-step creation flows
		{
			Name:        "conduit_start_builder",
			Description: "Start a step-by-step creation wizard for agents, skills, or prompt templates. NOT for asking arbitrary questions — only for creating new entities. You MUST provide builder_name (agent, skill, or prompt_template). After starting, use mentat_builder_step for each subsequent step.",
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
