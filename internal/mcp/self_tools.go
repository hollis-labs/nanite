package mcp

import "github.com/hollis-labs/mentat-chat/internal/provider"

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
			Name:        "mentat_create_skill",
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
			Name:        "mentat_list_skills",
			Description: "List all skills. Returns name, slug, category, and description for each.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"category": map[string]any{"type": "string", "description": "Filter by category (optional)"},
				},
			},
		},
		{
			Name:        "mentat_update_skill",
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
			Name:        "mentat_delete_skill",
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
			Name:        "mentat_create_agent",
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
			Name:        "mentat_list_agents",
			Description: "List all agent profiles. Returns name, slug, description, and default model.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "mentat_update_agent",
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
			Name:        "mentat_list_workflows",
			Description: "List all workflows. Returns name, slug, trigger, and enabled status.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "mentat_create_workflow",
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
	}
}
