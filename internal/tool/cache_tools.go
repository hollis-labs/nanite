package tool

import (
	"fmt"
	"strings"
)

// IsResultCacheTool reports whether name is a cache-navigation tool.
func IsResultCacheTool(name string) bool {
	return name == "fetch_tool_result" || name == "search_tool_result"
}

// FetchToolResult serves the fetch_tool_result tool: one page of a cached
// result, scoped to sessionID. Shared by the in-process chat loop and the
// self-tool HTTP proxy so both retrieve identically.
func (c *ResultCache) FetchToolResult(sessionID string, input map[string]any, budget int) (string, bool) {
	id, _ := input["id"].(string)
	if id == "" {
		return "Error: 'id' is required", true
	}
	pointer, _ := input["json_pointer"].(string)
	offset, err := cacheInt(input, "offset", 0)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	length, err := cacheInt(input, "length", budget)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	page, err := c.ReadPage(sessionID, id, pointer, offset, length, budget)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	header := fmt.Sprintf("[Cached result %s; json_pointer=%q; bytes %d..%d of %d (end-exclusive); has_more=%t; next_offset=%d]\n\n",
		id, pointer, page.Offset, page.End, page.TotalBytes, page.HasMore, page.End)
	return header + page.Content, false
}

// SearchToolResult serves the search_tool_result tool.
func (c *ResultCache) SearchToolResult(sessionID string, input map[string]any, budget int) (string, bool) {
	id, _ := input["id"].(string)
	pattern, _ := input["pattern"].(string)
	if id == "" || pattern == "" {
		return "Error: 'id' and 'pattern' are required", true
	}
	pointer, _ := input["json_pointer"].(string)
	offset, err := cacheInt(input, "offset", 0)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	maximum, err := cacheInt(input, "max_matches", 20)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	page, err := c.SearchPage(sessionID, id, pointer, pattern, offset, maximum, budget)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	var out strings.Builder
	fmt.Fprintf(&out, "[Cached result %s; json_pointer=%q; matching lines=%d; total_bytes=%d; has_more=%t; next_offset=%d]\n", id, pointer, len(page.Matches), page.TotalBytes, page.HasMore, page.NextOffset)
	for _, m := range page.Matches {
		fmt.Fprintf(&out, "\n--- Line %d; match bytes %d..%d; context bytes %d..%d; context_truncated=%t ---\n%s\n", m.Line, m.MatchOffset, m.MatchEnd, m.ContextOffset, m.ContextEnd, m.ContextTruncated, m.Context)
	}
	if len(page.Matches) == 0 {
		out.WriteString("No matching lines.\n")
	}
	return out.String(), false
}

func cacheInt(input map[string]any, key string, fallback int) (int, error) {
	value, exists := input[key]
	if !exists || value == nil {
		return fallback, nil
	}
	var number float64
	switch v := value.(type) {
	case float64:
		number = v
	case int:
		number = float64(v)
	default:
		return 0, fmt.Errorf("%s must be a non-negative integer", key)
	}
	if number < 0 || number > float64(1<<30) || number != float64(int(number)) {
		return 0, fmt.Errorf("%s must be a non-negative integer no greater than 1073741824", key)
	}
	return int(number), nil
}

// IsCacheExemptTool reports whether name is a tool whose output should bypass
// the result-cache soft truncation. These are agent-discovery and
// cache-navigation primitives whose value is providing actionable inline
// content; routing them through the cache produces pointer-to-pointer
// indirection that wastes the agent's turn budget.
//
// CW-20260429-0029 (c114 follow-up): tool_describe's 6813-byte
// response was being soft-truncated at 2 KiB by default, forcing the agent
// to navigate via fetch_tool_result / search_tool_result. The describe gate
// (CW-20260429-0025) made this hot path because describe is now required
// before card_show, and the agent burned all 10 turns shuffling
// pointers instead of emitting an envelope.
//
// Exempted tools:
//   - tool_describe — discovery contract; agent must read inline.
//   - tool_validate     — pre-flight validator; structured findings the
//     agent acts on directly.
//   - fetch_tool_result   — already returns a slice from the cache;
//     re-caching it produces a pointer of a pointer.
//   - search_tool_result  — same.
func IsCacheExemptTool(name string) bool {
	switch name {
	case "tool_describe",
		"tool_validate",
		"fetch_tool_result",
		"search_tool_result":
		return true
	}
	return false
}
