package toolclient

import (
	"encoding/json"
	"log/slog"
	"path"
	"strings"
)

// DefaultMaxCallsPerTurn is the default limit on tool calls per LLM turn.
const DefaultMaxCallsPerTurn = 25

// ToolPermissions defines allow/deny rules for an agent's tool access.
type ToolPermissions struct {
	AllowList          []string `json:"allow_list,omitempty"`
	DenyList           []string `json:"deny_list,omitempty"`
	MaxCallsPerTurn    int      `json:"max_calls_per_turn,omitempty"`
	AllowDelegation    bool     `json:"allow_delegation,omitempty"`
	AllowCodeExecution bool     `json:"allow_code_execution,omitempty"`
}

// UnmarshalJSON implements custom JSON unmarshalling for ToolPermissions.
// It accepts both canonical field names ("allow_list", "deny_list") and the
// shorthand variants ("allow", "deny"), merging values from both if present.
func (p *ToolPermissions) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	// Helper to decode a string slice from a raw JSON field.
	decodeList := func(key string) ([]string, error) {
		v, ok := raw[key]
		if !ok {
			return nil, nil
		}
		var list []string
		if err := json.Unmarshal(v, &list); err != nil {
			return nil, err
		}
		return list, nil
	}

	// allow_list (canonical)
	canonical, err := decodeList("allow_list")
	if err != nil {
		return err
	}
	p.AllowList = append(p.AllowList, canonical...)

	// allow (shorthand — warn)
	shorthand, err := decodeList("allow")
	if err != nil {
		return err
	}
	if len(shorthand) > 0 {
		slog.Warn("toolclient: deprecated field \"allow\" in tool_permissions — use \"allow_list\" instead")
		p.AllowList = append(p.AllowList, shorthand...)
	}

	// deny_list (canonical)
	canonical, err = decodeList("deny_list")
	if err != nil {
		return err
	}
	p.DenyList = append(p.DenyList, canonical...)

	// deny (shorthand — warn)
	shorthand, err = decodeList("deny")
	if err != nil {
		return err
	}
	if len(shorthand) > 0 {
		slog.Warn("toolclient: deprecated field \"deny\" in tool_permissions — use \"deny_list\" instead")
		p.DenyList = append(p.DenyList, shorthand...)
	}

	// max_calls_per_turn
	if v, ok := raw["max_calls_per_turn"]; ok {
		if err := json.Unmarshal(v, &p.MaxCallsPerTurn); err != nil {
			return err
		}
	}

	// allow_delegation
	if v, ok := raw["allow_delegation"]; ok {
		if err := json.Unmarshal(v, &p.AllowDelegation); err != nil {
			return err
		}
	}

	// allow_code_execution
	if v, ok := raw["allow_code_execution"]; ok {
		if err := json.Unmarshal(v, &p.AllowCodeExecution); err != nil {
			return err
		}
	}

	return nil
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
		if MatchPattern(pattern, toolName) {
			return false
		}
	}

	// If allow list is set, tool must match at least one pattern.
	if len(p.AllowList) > 0 {
		for _, pattern := range p.AllowList {
			if MatchPattern(pattern, toolName) {
				return true
			}
		}
		return false
	}

	return true
}

// MatchPattern checks if a tool name matches a glob pattern.
// Supports path.Match syntax plus simple prefix matching with trailing *.
func MatchPattern(pattern, name string) bool {
	// Handle prefix glob: "mcp__engine__*" matches "mcp__engine__engine_task_create"
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
