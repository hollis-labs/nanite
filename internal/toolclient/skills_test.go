package toolclient

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSkillsFromDir_Testdata(t *testing.T) {
	skills, err := LoadSkillsFromDir("testdata/skills")
	if err != nil {
		t.Fatalf("LoadSkillsFromDir: %v", err)
	}
	if len(skills) < 2 {
		t.Fatalf("expected >= 2 skills under testdata/skills, got %d", len(skills))
	}

	var explore, audit *ToolPreferenceSkill
	for i := range skills {
		switch {
		case skills[i].Pattern == "re:(search code|investigate|find function|explore codebase)":
			explore = &skills[i]
		case skills[i].Pattern == "re:audit (tasks|backlog|sprint)":
			audit = &skills[i]
		}
	}
	if explore == nil {
		t.Fatal("missing codebase-exploration skill")
	}
	if explore.Weight != 7 {
		t.Errorf("explore weight=%d want 7", explore.Weight)
	}
	if len(explore.Prefer) != 3 || explore.Prefer[0] != "dev_glob" {
		t.Errorf("explore prefer list mismatch: %v", explore.Prefer)
	}

	if audit == nil {
		t.Fatal("missing task-management skill")
	}
	if audit.compiledRegex == nil {
		t.Fatal("audit skill should have compiled regex (re: prefix)")
	}
	if !audit.compiledRegex.MatchString("audit tasks for sprint 8") {
		t.Errorf("audit regex should match 'audit tasks for sprint 8'")
	}
	if audit.compiledRegex.MatchString("review unrelated thing") {
		t.Errorf("audit regex should NOT match 'review unrelated thing'")
	}
}

func TestLoadSkillsFromDir_AbsentIsSilent(t *testing.T) {
	skills, err := LoadSkillsFromDir(filepath.Join(t.TempDir(), "definitely-not-there"))
	if err != nil {
		t.Fatalf("expected nil error for absent dir, got %v", err)
	}
	if skills != nil {
		t.Errorf("expected nil skills slice, got %v", skills)
	}
}

func TestMatchingSkills_SubstringAndRegex(t *testing.T) {
	skills, err := LoadSkillsFromDir("testdata/skills")
	if err != nil {
		t.Fatalf("LoadSkillsFromDir: %v", err)
	}

	cases := []struct {
		intent    string
		wantMatch int
		wantToolInPrefer string
	}{
		{"search code for handleRequestTools", 1, "dev_grep"},
		{"investigate broker selection", 1, "dev_glob"},
		{"audit tasks in current sprint", 1, "clockwork_task_get"},
		{"unrelated marketing copy", 0, ""},
	}
	for _, tc := range cases {
		got := MatchingSkills(skills, tc.intent)
		if len(got) != tc.wantMatch {
			t.Errorf("intent=%q got %d matches, want %d", tc.intent, len(got), tc.wantMatch)
			continue
		}
		if tc.wantMatch == 0 {
			continue
		}
		seen := false
		for _, name := range got[0].Prefer {
			if name == tc.wantToolInPrefer {
				seen = true
				break
			}
		}
		if !seen {
			t.Errorf("intent=%q matched skill %q but prefer list missing %q (got %v)",
				tc.intent, got[0].Source, tc.wantToolInPrefer, got[0].Prefer)
		}
	}
}

func TestMatchingSkills_WildcardIntent(t *testing.T) {
	skills := []ToolPreferenceSkill{{Pattern: "anything", Prefer: []string{"x"}}}
	for _, intent := range []string{"", "*"} {
		if got := MatchingSkills(skills, intent); len(got) != 0 {
			t.Errorf("intent=%q should not fire any skill, got %v", intent, got)
		}
	}
}

func TestParseSkillFile_MalformedFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.tools.preferences.md")
	if err := os.WriteFile(path, []byte("no frontmatter here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := parseSkillFile(path); err == nil {
		t.Error("expected error from frontmatter-less file")
	}
}

func TestSlugifyForVanta(t *testing.T) {
	cases := map[string]string{
		"Audit Tasks for Sprint":        "audit_tasks_for_sprint",
		"investigate-broker-selection":  "investigate_broker_selection",
		"   ":                           "general",
		"!!!":                           "general",
		"a/b/c":                         "a_b_c",
	}
	for in, want := range cases {
		if got := slugifyForVanta(in); got != want {
			t.Errorf("slugifyForVanta(%q) = %q, want %q", in, got, want)
		}
	}
}
