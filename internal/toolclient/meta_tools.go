package toolclient

import (
	"encoding/json"
	"fmt"
	"log"
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
			log.Printf("toolclient: request_tools intent=%q matched %d tools", s, len(byIntent))
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
