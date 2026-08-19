package toolclient

import (
	"path"
	"strings"
)

// MatchPattern checks if a tool name matches a glob pattern.
// Supports path.Match syntax plus simple prefix matching with trailing *.
//
// Tool names are uniform on the agent-facing surface (ADR-002), so a
// glob like "hadron_*" matches an MCP-published `hadron_run_enqueue`
// directly, without the legacy `mcp__server__` prefix.
func MatchPattern(pattern, name string) bool {
	// Handle prefix glob: "hadron_*" matches "hadron_run_enqueue".
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(name, strings.TrimSuffix(pattern, "*"))
	}
	matched, _ := path.Match(pattern, name)
	return matched
}

// ArgsContainEscalationPattern returns true if the arg map contains a value
// matching a known escalation pattern (e.g., path traversal). This is a
// conservative safety net for call sites where the policy layer does not yet
// support arg-level predicates: it rejects obvious bypass shapes regardless
// of the configured allow/deny list.
//
// Current patterns:
//   - Any string value (or string element in a slice) containing ".."
//     (path traversal, including "..", "../", "..\\", "foo/../bar").
//
// The check is recursive across nested maps and slices.
func ArgsContainEscalationPattern(args map[string]any) bool {
	for _, v := range args {
		if valueContainsEscalation(v) {
			return true
		}
	}
	return false
}

func valueContainsEscalation(v any) bool {
	switch t := v.(type) {
	case string:
		return strings.Contains(t, "..")
	case []any:
		for _, e := range t {
			if valueContainsEscalation(e) {
				return true
			}
		}
	case []string:
		for _, e := range t {
			if strings.Contains(e, "..") {
				return true
			}
		}
	case map[string]any:
		for _, e := range t {
			if valueContainsEscalation(e) {
				return true
			}
		}
	}
	return false
}
