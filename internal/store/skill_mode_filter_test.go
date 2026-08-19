package store

import (
	"testing"
)

// TestParseSkillModeIDs covers the empty/invalid/valid permutations.
func TestParseSkillModeIDs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty string", "", nil},
		{"empty array", "[]", nil},
		{"null", "null", nil},
		{"whitespace", "   ", nil},
		{"invalid", "not-json", nil},
		{"single", `["plan"]`, []string{"plan"}},
		{"multi", `["plan","work"]`, []string{"plan", "work"}},
		{"trims blanks", `["plan",""," "]`, []string{"plan"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseSkillModeIDs(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("len mismatch: got %v, want %v", got, tc.want)
			}
			for i, g := range got {
				if g != tc.want[i] {
					t.Fatalf("idx %d: got %q want %q", i, g, tc.want[i])
				}
			}
		})
	}
}

// TestMarshalSkillModeIDs round-trips through ParseSkillModeIDs.
func TestMarshalSkillModeIDs(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"nil", nil, "[]"},
		{"empty", []string{}, "[]"},
		{"single", []string{"plan"}, `["plan"]`},
		{"strips blanks", []string{"plan", " ", ""}, `["plan"]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MarshalSkillModeIDs(tc.in)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

// TestSkillMatchesMode covers the back-compat (empty) edge cases.
func TestSkillMatchesMode(t *testing.T) {
	cases := []struct {
		name        string
		skillModes  string
		currentMode string
		want        bool
	}{
		{"no current mode → match", `["plan"]`, "", true},
		{"empty skill modes → match", `[]`, "plan-id", true},
		{"matches", `["plan-id"]`, "plan-id", true},
		{"does not match", `["plan-id"]`, "work-id", false},
		{"matches one of many", `["plan-id","work-id"]`, "work-id", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SkillMatchesMode(tc.skillModes, tc.currentMode)
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

// Phase 0 item 21 ("Cut Modes, in full") deleted FilterSkillsByMode (the
// two-pass mode-binding + mode-tool_overrides pipeline) along with
// store.ToolOverrideSpec — there is no more session.current_mode_id to
// filter skills by. The tests that used to exercise FilterSkillsByMode's
// two passes were removed with it; ParseSkillModeIDs / MarshalSkillModeIDs
// / SkillMatchesMode remain real, independently-useful primitives over the
// skills.mode_ids column (see skill_mode_filter.go's file-level comment)
// and keep their coverage above.
