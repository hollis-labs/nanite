package contextbroker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// EngineSource retrieves context from Engine via MCP tools.
// It fetches current task/sprint/epic state for the active project.
type EngineSource struct {
	MCP        MCPCaller
	ServerName string // MCP server name (default: "engine")
}

// NewEngineSource creates a EngineSource with the given MCP caller.
func NewEngineSource(mcp MCPCaller) *EngineSource {
	return &EngineSource{
		MCP:        mcp,
		ServerName: "engine",
	}
}

func (s *EngineSource) Name() string { return "engine" }

func (s *EngineSource) Fetch(ctx context.Context, intent Intent, budget int) ([]ContextItem, error) {
	if s.MCP == nil {
		return nil, fmt.Errorf("engine source: no MCP caller configured")
	}

	var items []ContextItem
	usedTokens := 0

	// Fetch active tasks for the project.
	if intent.Scope != "" {
		taskItems, err := s.fetchTasks(ctx, intent.Scope, budget-usedTokens)
		if err != nil {
			log.Printf("contextbroker/engine: tasks fetch failed: %v", err)
		} else {
			for _, item := range taskItems {
				if usedTokens+item.TokenEstimate > budget {
					break
				}
				items = append(items, item)
				usedTokens += item.TokenEstimate
			}
		}
	}

	// Fetch active sprints if budget allows.
	if intent.Scope != "" && usedTokens < budget {
		sprintItems, err := s.fetchSprints(ctx, intent.Scope, budget-usedTokens)
		if err != nil {
			log.Printf("contextbroker/engine: sprints fetch failed: %v", err)
		} else {
			for _, item := range sprintItems {
				if usedTokens+item.TokenEstimate > budget {
					break
				}
				items = append(items, item)
				usedTokens += item.TokenEstimate
			}
		}
	}

	return items, nil
}

// fetchTasks retrieves tasks from Engine for a project.
func (s *EngineSource) fetchTasks(ctx context.Context, projectID string, budget int) ([]ContextItem, error) {
	toolName := fmt.Sprintf("mcp__%s__engine_tasks_list", s.ServerName)
	input := map[string]any{
		"project_id": projectID,
		"status":     "doing",
	}

	result, err := s.MCP.ExecuteTool(ctx, toolName, input)
	if err != nil {
		return nil, fmt.Errorf("engine_tasks_list: %w", err)
	}

	return s.parseTasks(result, budget, 0.8)
}

// fetchSprints retrieves active sprints from Engine.
func (s *EngineSource) fetchSprints(ctx context.Context, projectID string, budget int) ([]ContextItem, error) {
	toolName := fmt.Sprintf("mcp__%s__engine_sprints_list", s.ServerName)
	input := map[string]any{
		"project_id": projectID,
	}

	result, err := s.MCP.ExecuteTool(ctx, toolName, input)
	if err != nil {
		return nil, fmt.Errorf("engine_sprints_list: %w", err)
	}

	return s.parseSprints(result, budget)
}

// parseTasks converts Engine task list response into ContextItems.
func (s *EngineSource) parseTasks(raw string, budget int, baseRelevance float64) ([]ContextItem, error) {
	var response struct {
		Tasks []struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Status      string `json:"status"`
			Priority    string `json:"priority"`
			SprintID    string `json:"sprint_id"`
		} `json:"tasks"`
	}

	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		// Return raw as single item.
		tokens := EstimateTokens(raw)
		if tokens > budget {
			return nil, nil
		}
		return []ContextItem{{
			Source:        "engine",
			Key:           "tasks-raw",
			Content:       raw,
			TokenEstimate: tokens,
			Relevance:     0.5,
		}}, nil
	}

	var items []ContextItem
	usedTokens := 0

	for _, t := range response.Tasks {
		var sb strings.Builder
		fmt.Fprintf(&sb, "**%s** (%s)\n", t.Title, t.Status)
		if t.Priority != "" {
			fmt.Fprintf(&sb, "Priority: %s\n", t.Priority)
		}
		if t.Description != "" {
			fmt.Fprintf(&sb, "%s\n", t.Description)
		}

		content := sb.String()
		tokens := EstimateTokens(content)
		if usedTokens+tokens > budget {
			break
		}

		relevance := baseRelevance
		if t.Status == "doing" {
			relevance += 0.1
		}

		items = append(items, ContextItem{
			Source:        "engine",
			Key:           t.ID,
			Content:       content,
			TokenEstimate: tokens,
			Relevance:     relevance,
			Metadata: map[string]string{
				"status":   t.Status,
				"priority": t.Priority,
			},
		})
		usedTokens += tokens
	}

	return items, nil
}

// parseSprints converts Engine sprint list response into ContextItems.
func (s *EngineSource) parseSprints(raw string, budget int) ([]ContextItem, error) {
	var response struct {
		Sprints []struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Status string `json:"status"`
			Goal   string `json:"goal"`
		} `json:"sprints"`
	}

	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		return nil, err
	}

	var items []ContextItem
	usedTokens := 0

	for _, sp := range response.Sprints {
		if sp.Status != "active" && sp.Status != "planning" {
			continue
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "**Sprint: %s** (%s)\n", sp.Title, sp.Status)
		if sp.Goal != "" {
			fmt.Fprintf(&sb, "Goal: %s\n", sp.Goal)
		}

		content := sb.String()
		tokens := EstimateTokens(content)
		if usedTokens+tokens > budget {
			break
		}

		items = append(items, ContextItem{
			Source:        "engine",
			Key:           sp.ID,
			Content:       content,
			TokenEstimate: tokens,
			Relevance:     0.6,
			Metadata: map[string]string{
				"type":   "sprint",
				"status": sp.Status,
			},
		})
		usedTokens += tokens
	}

	return items, nil
}
