package toolclient

import (
	"encoding/json"
	"path"
	"strings"
)

// DefaultMaxCallsPerTurn is the default limit on tool calls per LLM turn.
const DefaultMaxCallsPerTurn = 25

// ToolPermissions defines allow/deny rules for an agent's tool access.
type ToolPermissions struct {
	AllowList       []string `json:"allow_list,omitempty"`
	DenyList        []string `json:"deny_list,omitempty"`
	MaxCallsPerTurn int      `json:"max_calls_per_turn,omitempty"`
}

// ParsePermissions parses a ToolPermissions from a JSON string.
// Returns a permissive default if the input is empty or invalid.
func ParsePermissions(raw string) ToolPermissions {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return ToolPermissions{MaxCallsPerTurn: DefaultMaxCallsPerTurn}
	}

	var perms ToolPermissions
	if err := json.Unmarshal([]byte(raw), &perms); err != nil {
		return ToolPermissions{MaxCallsPerTurn: DefaultMaxCallsPerTurn}
	}

	if perms.MaxCallsPerTurn <= 0 {
		perms.MaxCallsPerTurn = DefaultMaxCallsPerTurn
	}

	return perms
}

// CheckPermission returns true if the given tool name is allowed.
// Logic:
//   - If deny list is set and tool matches, deny.
//   - If allow list is set and tool does NOT match, deny.
//   - Otherwise, allow.
func (p ToolPermissions) CheckPermission(toolName string) bool {
	// Check deny list first — deny takes precedence.
	for _, pattern := range p.DenyList {
		if matchPattern(pattern, toolName) {
			return false
		}
	}

	// If allow list is set, tool must match at least one pattern.
	if len(p.AllowList) > 0 {
		for _, pattern := range p.AllowList {
			if matchPattern(pattern, toolName) {
				return true
			}
		}
		return false
	}

	return true
}

// matchPattern checks if a tool name matches a glob pattern.
// Supports path.Match syntax plus simple prefix matching with trailing *.
func matchPattern(pattern, name string) bool {
	// Handle prefix glob: "mcp__volon__*" matches "mcp__volon__task_create"
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(name, strings.TrimSuffix(pattern, "*"))
	}
	matched, _ := path.Match(pattern, name)
	return matched
}
