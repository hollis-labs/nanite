package toolclient

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// RequestToolsMetaTool returns the llmtypes.ToolDefinition for the
// request_tools meta-tool. The LLM can call this to request specific
// tools by name or by intent description.
func RequestToolsMetaTool() llmtypes.ToolDefinition {
	return llmtypes.ToolDefinition{
		Name: "request_tools",
		Description: "Load additional tool schemas into context by name or by intent description, then call them.\n\n" +
			"**When to use:** When you need a tool that is not currently in your context window (progressive discovery). Call with tool_names for exact lookups, or intent for semantic search when you're not sure of the exact name.\n\n" +
			"**When NOT to use:** Do not call this if the tool you want is already available in your context — calling request_tools for tools already loaded is a no-op and wastes a round-trip. Do not use intent search for tools you know by name.\n\n" +
			"**Output shape:** Summary line (\"Loaded N tool(s): name1, name2\") followed by a JSON array of {name, description, input_schema} objects. You can call any returned tool immediately after.\n\n" +
			"**Chaining:** This tool loads schemas; immediately follow with the actual tool call once the schema is returned.",
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
func (tb *ToolClient) HandleRequestTools(input map[string]any) ([]llmtypes.ToolDefinition, string) {
	var byName []llmtypes.ToolDefinition
	var byIntent []llmtypes.ToolDefinition

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
	var merged []llmtypes.ToolDefinition
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
func FetchToolResultMetaTool() llmtypes.ToolDefinition {
	return llmtypes.ToolDefinition{
		Name: "fetch_tool_result",
		Description: "Read a page of a cached result from this chat session. Use the bare ID from a tool_result:// pointer when a preview is incomplete. " +
			"Omit json_pointer to read the original response; use an RFC 6901 pointer such as /data/comments/3 or /stdout to select a JSON value. Strings are returned as decoded text. " +
			"Offsets address UTF-8 bytes in the selected value, not the preview. Returns actual start/end offsets, total bytes, has_more and next_offset. " +
			"Follow next_offset with the same id and json_pointer until you have the evidence needed. The default and maximum page size use the current model's result budget. " +
			"IDs are session-scoped cache IDs, not file paths or permanent resource identifiers.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "The cached result ULID, taken verbatim from a `tool_result://<ULID>` footer in this session. NOT a file path, NOT a cache:// URI — a bare ULID string only.",
				},
				"json_pointer": map[string]any{
					"type": "string", "description": "Optional RFC 6901 pointer into the original JSON, e.g. /stdout or /data/comments/3/content. Empty/omitted reads the original result. Escape ~ as ~0 and / as ~1 within a key.",
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Byte offset to start reading from (default: 0). Use to page through large results.",
				},
				"length": map[string]any{
					"type":        "integer",
					"description": "Requested bytes. Defaults to, and is capped by, the current model-aware result budget.",
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
func SearchToolResultMetaTool() llmtypes.ToolDefinition {
	return llmtypes.ToolDefinition{
		Name: "search_tool_result",
		Description: "Search cached result text for RE2 regex matches in this session. Use the bare ID from a tool_result:// pointer. " +
			"Optional json_pointer selects a JSON value, decoding strings such as /stdout; otherwise search the original response. " +
			"Returns matching lines with bounded surrounding context and byte coordinates usable by fetch_tool_result with the same json_pointer. " +
			"has_more and next_offset report whether further matching lines were omitted. Continue with offset=next_offset. " +
			"A preview or limited search is not evidence that the whole result has been read. Use filesystem tools for source files.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "The cached result ULID, taken verbatim from a `tool_result://<ULID>` footer in this session. NOT a file path — a bare ULID string only.",
				},
				"pattern": map[string]any{
					"type":        "string",
					"description": "RE2 regex pattern to search for. Case-sensitive by default. Use (?i) prefix for case-insensitive.",
				},
				"json_pointer": map[string]any{
					"type": "string", "description": "Optional RFC 6901 pointer selecting the same value as fetch_tool_result. Strings are decoded before searching.",
				},
				"offset": map[string]any{
					"type": "integer", "description": "Non-negative byte offset in the selected value; use next_offset to continue a limited search.",
				},
				"max_matches": map[string]any{
					"type":        "integer",
					"description": "Maximum matching lines (default 20, capped at 100). Context also obeys the current model-aware result budget; has_more reports further matches.",
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
