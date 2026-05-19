package toolclient

import (
	"encoding/json"
	"log/slog"
	"path"
	"strings"
)

// DefaultMaxCallsPerTurn is the default limit on tool calls per LLM turn
// surfaced through ToolPermissions.
//
// CW-20260519-0115 audit finding: this field is set on every parsed
// ToolPermissions value (here, in ParsePermissions, and in the
// broker.go fallback paths) but the runtime currently HAS NO ENFORCEMENT
// site that reads it as a count cap. The actual per-tool count
// enforcement lives in internal/service/chat_loop_state.go
// (defaultPerToolCap, sourced from UserSettings.ToolPerTurnCap;
// default 150, a high backstop). This constant is retained for
// back-compat with serialised ToolPermissions blobs (agent
// frontmatter, stored profiles) and for the agent-config layer that
// still accepts a `max_calls_per_turn:` YAML key, but until an
// enforcement site is wired (or the field is removed), the value here
// is decorative. Treat it as "if we ever enforce this, this is the
// number" — it should track the active enforcement cap so the two
// don't drift if the enforcement site is added back.
const DefaultMaxCallsPerTurn = 25

// ToolPermissions defines allow/deny rules for an agent's tool access.
//
// MaxCallsPerTurn is currently NOT enforced at runtime — see the
// DefaultMaxCallsPerTurn doc above for the audit finding. The
// enforcement-bearing per-tool cap lives in loopState.limits.defaultPerToolCap.
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
