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
		Description: "Retrieve a byte slice of a cached large tool result that was truncated in context.\n\n" +
			"**When to use:** When a previous tool result showed a truncation notice with a `tool_result://<ULID>` pointer at the footer, call this with that ULID to read more of the content. Useful for paging through large file listings, long API responses, or any tool output that exceeded the context cap.\n\n" +
			"**When NOT to use:** Do NOT pass a file path as the id — this tool reads from the in-memory result cache, not the filesystem. Do NOT guess an id; the id MUST come verbatim from a `tool_result://<ULID>` footer in the current session. Do NOT use `cache://` or any other URI scheme — the id is a bare ULID string (e.g. \"01HZ3G9MXKQ7D5FVWNTJ4BSEP6\").\n\n" +
			"**Output shape:** Raw bytes from the cached result, returned as text. If offset + length exceeds the cache size, only available bytes are returned. Returns an error if the ULID is not found in the cache.\n\n" +
			"**Chaining:** Pair with search_tool_result when you want to find a specific pattern instead of reading sequentially.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "The cached result ULID, taken verbatim from a `tool_result://<ULID>` footer in this session. NOT a file path, NOT a cache:// URI — a bare ULID string only.",
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Byte offset to start reading from (default: 0). Use to page through large results.",
				},
				"length": map[string]any{
					"type":        "integer",
					"description": "Number of bytes to read (default: 65536). Capped at the cache ceiling for the originating MCP trust tier.",
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
		Description: "Regex-search a cached large tool result and return matching lines with context (like grep -C 2).\n\n" +
			"**When to use:** When a previous tool result was truncated and you need to find a specific pattern (function name, error string, field key) without reading the entire cache sequentially. More efficient than fetch_tool_result + manual scanning for targeted lookups.\n\n" +
			"**When NOT to use:** Do NOT pass a file path as the id — this tool searches the in-memory result cache, not the filesystem. The id MUST be a bare ULID from a `tool_result://<ULID>` footer in this session. Do NOT use for full-text grep of source files — use dev_grep for filesystem searches.\n\n" +
			"**Output shape:** Up to max_matches blocks, each showing the matching line with 2 lines of surrounding context (configurable). Returns \"no matches\" if the pattern is not found. Returns an error if the ULID is not found in the cache.\n\n" +
			"**Chaining:** Use fetch_tool_result after search to read the surrounding region at a known offset. Use dev_grep for filesystem search instead.",
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
				"max_matches": map[string]any{
					"type":        "integer",
					"description": "Maximum number of match blocks to return (default: 20). Each block includes the matching line plus 2 lines of context.",
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
