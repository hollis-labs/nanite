package store

import (
	"encoding/json"
	"strings"
)

// E2 (CW-20260428-0017): mode-aware skill filtering. Two-pass pipeline:
//
//	Pass 1 — mode binding: keep skills whose mode_ids contains the current
//	         mode ID, or whose mode_ids is empty (back-compat: empty = all
//	         modes).
//	Pass 2 — mode tool_overrides (B1): apply the mode's tool_overrides
//	         allow/deny set on the result. Mode tool_overrides is
//	         authoritative — a skill listed in deny is excluded even when
//	         mode_ids matches the current mode.
//
// Precedence (locked 2026-04-28): mode tool_overrides wins over skill
// mode_ids. Rationale: skill mode_ids declares "where I'm appropriate";
// mode tool_overrides declares "what's actually loaded right now".
// Overrides override.

// ParseSkillModeIDs decodes the JSON-array string stored in skills.mode_ids.
// Empty / "[]" / invalid JSON returns nil with no error so callers can keep
// treating the result as "available in all modes".
func ParseSkillModeIDs(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" || raw == "null" {
		return nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// MarshalSkillModeIDs serializes a slice to the JSON-array string form
// expected by the skills.mode_ids column. nil / empty produces "[]" so the
// callers do not have to special-case empty values when persisting.
func MarshalSkillModeIDs(ids []string) string {
	if len(ids) == 0 {
		return "[]"
	}
	clean := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			clean = append(clean, id)
		}
	}
	if len(clean) == 0 {
		return "[]"
	}
	b, err := json.Marshal(clean)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// SkillMatchesMode reports whether a skill is bound to the given mode ID.
// Empty modeID (no current mode) returns true → fall back to "load all"
// (back-compat). Empty mode_ids on the skill returns true → "available
// everywhere".
func SkillMatchesMode(skillModeIDs string, modeID string) bool {
	if modeID == "" {
		return true
	}
	ids := ParseSkillModeIDs(skillModeIDs)
	if len(ids) == 0 {
		return true
	}
	for _, id := range ids {
		if id == modeID {
			return true
		}
	}
	return false
}

// FilterSkillsByMode runs the two-pass pipeline described at the top of the
// file and returns the kept skills. modeID is the resolved
// session.current_mode_id (empty = no mode set; behavior is back-compat
// "keep everything"). modeToolOverrides is the parsed tool override spec
// from the same mode; isEmpty / zero-value spec is a passthrough.
//
// The skill is matched against tool_overrides by slug. The skill slug is
// the canonical handle — the mode's tool_overrides Allow/Deny lists treat
// each entry as either a tool name (for B1) or a skill slug (here). This
// keeps the contract obvious: "deny: [foo]" denies the skill named foo.
func FilterSkillsByMode(skills []Skill, modeID string, modeToolOverrides ToolOverrideSpec) []Skill {
	out := make([]Skill, 0, len(skills))

	denySet := stringSet(modeToolOverrides.Deny)
	allowSet := stringSet(modeToolOverrides.Allow)
	hasOverrides := !isEmptyToolOverrideSpec(modeToolOverrides)

	for _, sk := range skills {
		// Pass 1: mode binding.
		if !SkillMatchesMode(sk.ModeIDs, modeID) {
			continue
		}
		// Pass 2: mode tool_overrides — precedence wins over Pass 1.
		if hasOverrides {
			// Deny is absolute (mirrors ApplyToolOverrides rule 1).
			if _, denied := denySet[sk.Slug]; denied {
				continue
			}
			// Explicit allow keeps the skill regardless of patterns.
			if _, allowed := allowSet[sk.Slug]; allowed {
				out = append(out, sk)
				continue
			}
			// DenyPatterns drops if matched.
			if matchesAnyPattern(sk.Slug, modeToolOverrides.DenyPatterns) {
				continue
			}
			// AllowPatterns whitelist mode: when non-empty, drop non-matches.
			if len(modeToolOverrides.AllowPatterns) > 0 &&
				!matchesAnyPattern(sk.Slug, modeToolOverrides.AllowPatterns) {
				continue
			}
		}
		out = append(out, sk)
	}
	return out
}
