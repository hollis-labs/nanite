package fragmentsengine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/nanite/internal/mcp"
)

// SprintToolsTransport implements mcp.MCPTransport and exposes sprint planning
// tools via the Engine MCP integration.
type SprintToolsTransport struct {
	mcpManager *mcp.Manager
}

func NewSprintToolsTransport(mgr *mcp.Manager) *SprintToolsTransport {
	return &SprintToolsTransport{mcpManager: mgr}
}

func (t *SprintToolsTransport) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	return []mcp.Tool{
		{
			Name:        "nanite_open_sprint_planning",
			Description: "Open the sprint planning modal in the UI. Use when the user asks to review sprints, plan work, or manage tasks and backlog.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project_id": map[string]any{"type": "string", "description": "Optional project ID to scope the view (omit for all projects)"},
				},
			},
		},
		{
			Name:        "nanite_show_sprint_planning_review",
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
	}, nil
}

func (t *SprintToolsTransport) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.ToolResult, error) {
	switch name {
	case "nanite_open_sprint_planning":
		return textResult("Sprint planning modal opened in the UI."), nil
	case "nanite_show_sprint_planning_review":
		return t.callShowSprintPlanningReview(args)
	default:
		return mcpErrorResult(fmt.Sprintf("unknown tool: %s", name)), nil
	}
}

func (t *SprintToolsTransport) callShowSprintPlanningReview(args map[string]any) (*mcp.ToolResult, error) {
	title, _ := args["title"].(string)
	sprintsStr, _ := args["sprints"].(string)
	tasksStr, _ := args["tasks"].(string)
	if title == "" || sprintsStr == "" || tasksStr == "" {
		return mcpErrorResult("title, sprints, and tasks are required"), nil
	}

	var sprints []any
	if err := json.Unmarshal([]byte(sprintsStr), &sprints); err != nil {
		return mcpErrorResult(fmt.Sprintf("invalid sprints JSON: %v", err)), nil
	}
	var tasks []any
	if err := json.Unmarshal([]byte(tasksStr), &tasks); err != nil {
		return mcpErrorResult(fmt.Sprintf("invalid tasks JSON: %v", err)), nil
	}

	envData := map[string]any{
		"title":   title,
		"sprints": sprints,
		"tasks":   tasks,
	}
	if desc, _ := args["description"].(string); desc != "" {
		envData["description"] = desc
	}
	if ps, ok := args["page_size"].(float64); ok && ps > 0 {
		envData["page_size"] = int(ps)
	}

	envJSON, _ := json.Marshal(map[string]any{
		"kind":    "envelope",
		"version": 1,
		"type":    "sprint-planning-review",
		"data":    envData,
	})

	result := fmt.Sprintf("Sprint planning review card ready: %s (%d sprints, %d tasks)\n<!--ENVELOPE_DATA:%s:ENVELOPE_DATA-->",
		title, len(sprints), len(tasks), string(envJSON))
	return textResult(result), nil
}

func textResult(text string) *mcp.ToolResult {
	return &mcp.ToolResult{
		Content: []mcp.ToolContent{{Type: "text", Text: text}},
	}
}

func mcpErrorResult(msg string) *mcp.ToolResult {
	return &mcp.ToolResult{
		Content: []mcp.ToolContent{{Type: "text", Text: msg}},
		IsError: true,
	}
}
