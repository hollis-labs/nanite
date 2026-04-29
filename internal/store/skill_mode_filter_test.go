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

// TestFilterSkillsByMode_BackCompat confirms behavior is unchanged when no
// session.current_mode_id is set: every skill flows through.
func TestFilterSkillsByMode_BackCompat(t *testing.T) {
	skills := []Skill{
		{Slug: "alpha", ModeIDs: `["plan"]`},
		{Slug: "beta"}, // mode_ids empty
	}
	got := FilterSkillsByMode(skills, "", ToolOverrideSpec{})
	if len(got) != 2 {
		t.Fatalf("expected all 2 skills kept, got %d", len(got))
	}
}

// TestFilterSkillsByMode_Pass1 validates the mode-binding pass alone.
func TestFilterSkillsByMode_Pass1(t *testing.T) {
	skills := []Skill{
		{Slug: "plan-only", ModeIDs: `["plan"]`},
		{Slug: "work-only", ModeIDs: `["work"]`},
		{Slug: "everywhere", ModeIDs: `[]`},
		{Slug: "plan-and-work", ModeIDs: `["plan","work"]`},
	}

	plan := FilterSkillsByMode(skills, "plan", ToolOverrideSpec{})
	wantPlan := map[string]bool{"plan-only": true, "everywhere": true, "plan-and-work": true}
	if len(plan) != len(wantPlan) {
		t.Fatalf("plan: got %d skills, want %d (got %v)", len(plan), len(wantPlan), slugs(plan))
	}
	for _, s := range plan {
		if !wantPlan[s.Slug] {
			t.Fatalf("plan: unexpected skill %q kept", s.Slug)
		}
	}

	work := FilterSkillsByMode(skills, "work", ToolOverrideSpec{})
	wantWork := map[string]bool{"work-only": true, "everywhere": true, "plan-and-work": true}
	if len(work) != len(wantWork) {
		t.Fatalf("work: got %d skills, want %d (got %v)", len(work), len(wantWork), slugs(work))
	}
	for _, s := range work {
		if !wantWork[s.Slug] {
			t.Fatalf("work: unexpected skill %q kept", s.Slug)
		}
	}
}

// TestFilterSkillsByMode_Pass2_DenyWinsOverModeMatch is the precedence test
// from the acceptance criteria: a skill matching mode_ids BUT listed in mode
// tool_overrides.deny is NOT loaded.
func TestFilterSkillsByMode_Pass2_DenyWinsOverModeMatch(t *testing.T) {
	skills := []Skill{
		{Slug: "writing-skill", ModeIDs: `["plan"]`},
		{Slug: "drafting-skill", ModeIDs: `["plan"]`},
	}
	overrides := ToolOverrideSpec{
		Deny: []string{"writing-skill"},
	}

	got := FilterSkillsByMode(skills, "plan", overrides)
	if len(got) != 1 {
		t.Fatalf("expected 1 skill kept (drafting-skill), got %d (%v)", len(got), slugs(got))
	}
	if got[0].Slug != "drafting-skill" {
		t.Fatalf("expected drafting-skill kept, got %q", got[0].Slug)
	}
}

// TestFilterSkillsByMode_Pass2_AllowKeepsExplicit verifies that an
// allow-list keeps a skill that would otherwise be skipped by deny patterns.
func TestFilterSkillsByMode_Pass2_AllowKeepsExplicit(t *testing.T) {
	skills := []Skill{
		{Slug: "writer-outline", ModeIDs: `[]`},
		{Slug: "writer-draft", ModeIDs: `[]`},
	}
	overrides := ToolOverrideSpec{
		Allow:        []string{"writer-outline"},
		DenyPatterns: []string{"writer-*"},
	}

	got := FilterSkillsByMode(skills, "plan", overrides)
	if len(got) != 1 {
		t.Fatalf("expected 1 skill kept, got %d (%v)", len(got), slugs(got))
	}
	if got[0].Slug != "writer-outline" {
		t.Fatalf("expected writer-outline kept, got %q", got[0].Slug)
	}
}

// TestFilterSkillsByMode_Pass2_AllowPatternsWhitelist verifies AllowPatterns
// drops non-matching skills even when they pass Pass 1.
func TestFilterSkillsByMode_Pass2_AllowPatternsWhitelist(t *testing.T) {
	skills := []Skill{
		{Slug: "code-edit", ModeIDs: `["plan"]`},
		{Slug: "writer-draft", ModeIDs: `["plan"]`},
	}
	overrides := ToolOverrideSpec{
		AllowPatterns: []string{"writer-*"},
	}

	got := FilterSkillsByMode(skills, "plan", overrides)
	if len(got) != 1 {
		t.Fatalf("expected 1 skill kept, got %d (%v)", len(got), slugs(got))
	}
	if got[0].Slug != "writer-draft" {
		t.Fatalf("expected writer-draft kept, got %q", got[0].Slug)
	}
}

// TestFilterSkillsByMode_Pass1_DropsNonMatchEvenWithEmptyOverrides ensures
// the Pass-1 filter applies when there are no tool_overrides at all.
func TestFilterSkillsByMode_Pass1_DropsNonMatchEvenWithEmptyOverrides(t *testing.T) {
	skills := []Skill{
		{Slug: "plan-only", ModeIDs: `["plan"]`},
		{Slug: "work-only", ModeIDs: `["work"]`},
	}
	got := FilterSkillsByMode(skills, "plan", ToolOverrideSpec{})
	if len(got) != 1 || got[0].Slug != "plan-only" {
		t.Fatalf("expected plan-only kept, got %v", slugs(got))
	}
}

func slugs(in []Skill) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = s.Slug
	}
	return out
}
