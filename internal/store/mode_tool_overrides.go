package store

import (
	"encoding/json"
	"fmt"
	"path"
)

// ToolOverrideSpec is the parsed shape of Mode.ToolOverrides JSON.
// Empty slices mean "not specified" — callers should treat the spec as
// passthrough when every list is empty. (B1, CW-20260428-0009)
type ToolOverrideSpec struct {
	Allow         []string `json:"allow,omitempty"`
	Deny          []string `json:"deny,omitempty"`
	AllowPatterns []string `json:"allow_patterns,omitempty"`
	DenyPatterns  []string `json:"deny_patterns,omitempty"`
}

// ParseToolOverrides decodes a Mode.ToolOverrides JSON blob. Empty / "{}" /
// invalid JSON returns the zero-value spec with no error so callers can keep
// using the spec as passthrough.
func ParseToolOverrides(raw string) (ToolOverrideSpec, error) {
	var spec ToolOverrideSpec
	if raw == "" || raw == "{}" {
		return spec, nil
	}
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return spec, fmt.Errorf("parse tool overrides: %w", err)
	}
	return spec, nil
}

// ApplyToolOverrides filters a list of tool names per the spec.
// Resolution order (deny wins over allow, explicit names win over patterns):
//  1. If tool is in Deny → remove (always wins).
//  2. Else if tool is in Allow → keep (overrides DenyPatterns).
//  3. Else if tool matches any DenyPattern → remove.
//  4. Else if AllowPatterns is non-empty AND tool matches none → remove.
//  5. Else keep.
//
// Empty spec returns input unchanged. (B1, CW-20260428-0009)
func ApplyToolOverrides(tools []string, spec ToolOverrideSpec) []string {
	if isEmptyToolOverrideSpec(spec) {
		return tools
	}

	denySet := stringSet(spec.Deny)
	allowSet := stringSet(spec.Allow)

	out := make([]string, 0, len(tools))
	for _, name := range tools {
		// 1. Deny is absolute.
		if _, denied := denySet[name]; denied {
			continue
		}
		// 2. Explicit Allow beats DenyPatterns and the AllowPatterns whitelist.
		if _, allowed := allowSet[name]; allowed {
			out = append(out, name)
			continue
		}
		// 3. DenyPatterns drops if matched.
		if matchesAnyPattern(name, spec.DenyPatterns) {
			continue
		}
		// 4. AllowPatterns whitelist mode: when non-empty, drop non-matches.
		if len(spec.AllowPatterns) > 0 && !matchesAnyPattern(name, spec.AllowPatterns) {
			continue
		}
		// 5. Default keep.
		out = append(out, name)
	}
	return out
}

func isEmptyToolOverrideSpec(spec ToolOverrideSpec) bool {
	return len(spec.Allow) == 0 && len(spec.Deny) == 0 &&
		len(spec.AllowPatterns) == 0 && len(spec.DenyPatterns) == 0
}

func stringSet(in []string) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for _, s := range in {
		out[s] = struct{}{}
	}
	return out
}

// matchesAnyPattern returns true when name matches at least one glob pattern
// per Go's path.Match semantics. Malformed patterns are skipped silently —
// the spec author is the same trust tier as the agent profile, and we want
// a typo to fail open (don't block the tool) rather than panic the turn.
func matchesAnyPattern(name string, patterns []string) bool {
	for _, p := range patterns {
		if p == "" {
			continue
		}
		ok, err := path.Match(p, name)
		if err == nil && ok {
			return true
		}
	}
	return false
}
