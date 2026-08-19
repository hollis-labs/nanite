package store

import (
	"encoding/json"
	"strings"
)

// E2 (CW-20260428-0017): mode-aware skill filtering.
//
// Phase 0 item 21 ("Cut Modes, in full") deleted Session Mode (the
// sessions.current_mode_id pointer, the modes table, store.GetSessionMode)
// and the mode tool_overrides subsystem it fed (store.ToolOverrideSpec /
// store.ApplyToolOverrides), along with FilterSkillsByMode — the two-pass
// pipeline that used to combine skill.mode_ids binding with a mode's
// tool_overrides allow/deny set. There is no more session-level "current
// mode" to filter skills by.
//
// ParseSkillModeIDs / MarshalSkillModeIDs / SkillMatchesMode survive below
// — they're pure helpers over the skills.mode_ids column (unrelated to the
// now-deleted modes table) still consumed by
// internal/service/ingest.go's frontmatter → mode_ids resolution.
//
// Phase 0 item 22 (decision log §11): internal/skillbroker — the other
// consumer, whose mode-bound relevance bonus read SkillMatchesMode/
// ParseSkillModeIDs (a skill with any mode_ids tag scored slightly higher,
// regardless of session state) — is retired. SkillMatchesMode has no
// remaining caller in this app as of this cut; left in place (mirroring
// the tool_enrichments precedent from 18a-cut-dead-storage-and-config)
// since removing it is outside this task's scope.

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
