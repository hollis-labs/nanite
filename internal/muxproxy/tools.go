package muxproxy

import "github.com/hollis-labs/go-providers/provider"

// ToolDefinitions returns the four orchestrator tools in the
// provider.ToolDefinition format accepted by the tool broker.
func ToolDefinitions() []provider.ToolDefinition {
	return []provider.ToolDefinition{
		{
			Name:        "mux_list_launches",
			Description: "List available agent-mux launches (subordinate agent templates). Returns {launches:[{id,project,agent,provider}]}.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "mux_launch",
			Description: "Start a subordinate claudestream agent from a launch ID. Assign a human-readable nickname so you can reference it in replies.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"launch_id": map[string]any{"type": "string", "description": "Launch ID from mux_list_launches"},
					"nickname":  map[string]any{"type": "string", "description": "Short label (A, B, researcher, etc.)"},
				},
				"required": []string{"launch_id", "nickname"},
			},
		},
		{
			Name:        "mux_send",
			Description: "Send text to a subordinate and block until it finishes the turn. Returns full transcript + tool_uses. For parallel dispatch, issue multiple mux_send calls in one assistant turn.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id": map[string]any{"type": "string", "description": "Session ID from mux_launch"},
					"text":       map[string]any{"type": "string", "description": "Message to deliver"},
				},
				"required": []string{"session_id", "text"},
			},
		},
		{
			Name:        "mux_stop",
			Description: "Stop a subordinate session and free its nickname. Optional — cleanup runs on chat-session exit.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id": map[string]any{"type": "string", "description": "Session ID from mux_launch"},
				},
				"required": []string{"session_id"},
			},
		},
	}
}
