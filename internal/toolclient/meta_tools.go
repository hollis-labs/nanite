package toolclient

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/hollis-labs/go-providers/provider"
)

// RequestToolsMetaTool returns the provider.ToolDefinition for the
// request_tools meta-tool. The LLM can call this to request specific
// tools by name or by intent description.
func RequestToolsMetaTool() provider.ToolDefinition {
	return provider.ToolDefinition{
		Name:        "request_tools",
		Description: "Request tools by name or by describing what you want to do (intent). Returns full tool schemas that you can then call.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tool_names": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Specific tool names to load (exact match)",
				},
				"intent": map[string]any{
					"type":        "string",
					"description": "Describe what you want to do — the broker will find the most relevant tools",
				},
			},
			// additionalProperties: false is required for strict-mode compatibility.
			// Anthropic rejects strict:true tools whose schemas allow unknown fields.
			"additionalProperties": false,
		},
	}
}

// DefaultMaxIntentResults is the default cap on tools returned by intent search.
const DefaultMaxIntentResults = 10

// HandleRequestTools processes a request_tools meta-tool call.
// It resolves tools by exact name and/or by intent scoring, returning the
// matched tool definitions as a JSON-encoded result string.
func (tb *ToolClient) HandleRequestTools(input map[string]any) ([]provider.ToolDefinition, string) {
	var byName []provider.ToolDefinition
	var byIntent []provider.ToolDefinition

	// Resolve by explicit tool names.
	if names, ok := input["tool_names"]; ok {
		if nameList, ok := names.([]any); ok {
			var requested []string
			for _, n := range nameList {
				if s, ok := n.(string); ok {
					requested = append(requested, s)
				}
			}
			if len(requested) > 0 {
				byName = tb.GetToolsByNames(requested)
			}
		}
	}

	// Resolve by intent.
	if intentStr, ok := input["intent"]; ok {
		if s, ok := intentStr.(string); ok && s != "" {
			byIntent = tb.SelectByIntent(s, DefaultMaxIntentResults)
			slog.Info("toolclient: request_tools matched", "intent", s, "count", len(byIntent))
		}
	}

	// Merge results, deduplicating by name.
	seen := make(map[string]bool)
	var merged []provider.ToolDefinition
	for _, t := range byName {
		if !seen[t.Name] {
			seen[t.Name] = true
			merged = append(merged, t)
		}
	}
	for _, t := range byIntent {
		if !seen[t.Name] {
			seen[t.Name] = true
			merged = append(merged, t)
		}
	}

	// Build a human-readable summary.
	var names []string
	for _, t := range merged {
		names = append(names, t.Name)
	}

	var summary string
	if len(merged) > 0 {
		// Produce a compact JSON array of {name, description} for the LLM.
		type toolInfo struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"input_schema"`
		}
		infos := make([]toolInfo, len(merged))
		for i, t := range merged {
			infos[i] = toolInfo{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema}
		}
		data, _ := json.Marshal(infos)
		summary = fmt.Sprintf("Loaded %d tool(s): %s\n\n%s", len(merged), strings.Join(names, ", "), string(data))
	} else {
		summary = "No matching tools found."
	}

	return merged, summary
}

// FetchToolResultMetaTool returns the tool definition for the fetch_tool_result
// meta-tool. The LLM uses this to retrieve slices of cached large tool results.
func FetchToolResultMetaTool() provider.ToolDefinition {
	return provider.ToolDefinition{
		Name:        "fetch_tool_result",
		Description: "Retrieve a slice of a cached tool result by ID. Use when a previous tool result was truncated and you need more content.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "The cached result ID (from the truncation pointer)",
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Byte offset to start reading from (default: 0)",
				},
				"length": map[string]any{
					"type":        "integer",
					"description": "Number of bytes to read (default: 65536)",
				},
			},
			"required": []any{"id"},
			// additionalProperties: false required for strict-mode compatibility.
			"additionalProperties": false,
		},
	}
}

// SearchToolResultMetaTool returns the tool definition for the
// search_tool_result meta-tool. The LLM uses this to regex-search cached
// large tool results.
func SearchToolResultMetaTool() provider.ToolDefinition {
	return provider.ToolDefinition{
		Name:        "search_tool_result",
		Description: "Search a cached tool result by regex pattern. Returns matching lines with surrounding context (like grep -C 2).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "The cached result ID (from the truncation pointer)",
				},
				"pattern": map[string]any{
					"type":        "string",
					"description": "Regex pattern to search for",
				},
				"max_matches": map[string]any{
					"type":        "integer",
					"description": "Maximum number of matches to return (default: 20)",
				},
			},
			"required": []any{"id", "pattern"},
			// additionalProperties: false required for strict-mode compatibility.
			"additionalProperties": false,
		},
	}
}

// MetaToolNames returns the set of meta-tool names that should be exempt from
// the per-tool call cap.
func MetaToolNames() map[string]bool {
	return map[string]bool{
		"request_tools":      true,
		"fetch_tool_result":  true,
		"search_tool_result": true,
	}
}
