package store

import "testing"

func TestClassifySkillSource(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", SkillCategoryInternal},
		{"builtin", SkillCategoryInternal},
		{"seed", SkillCategoryInternal},
		{"user", SkillCategoryUser},
		{"project", SkillCategoryUser},
		{"plugin", SkillCategoryPlugin},
		{"claude", SkillCategoryOther},
		{"agentrc", SkillCategoryOther},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := ClassifySkillSource(tc.in)
			if got != tc.want {
				t.Fatalf("ClassifySkillSource(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}
