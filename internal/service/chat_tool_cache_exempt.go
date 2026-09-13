package service

func isResultCacheTool(name string) bool {
	return name == "fetch_tool_result" || name == "search_tool_result"
}

// isCacheExemptTool reports whether name is a tool whose output should bypass
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
func isCacheExemptTool(name string) bool {
	switch name {
	case "tool_describe",
		"tool_validate",
		"fetch_tool_result",
		"search_tool_result":
		return true
	}
	return false
}
