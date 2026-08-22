package skill

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEnsureHomeDirs_CreatesDir verifies that EnsureHomeDirs creates
// ~/.nanite/skills/ when it does not yet exist.
func TestEnsureHomeDirs_CreatesDir(t *testing.T) {
	home := t.TempDir()
	skillsDir := filepath.Join(home, ".nanite", "skills")

	// Precondition: directory must not exist.
	if _, err := os.Stat(skillsDir); err == nil {
		t.Fatal("skills dir already exists before EnsureHomeDirs — test setup error")
	}

	if err := EnsureHomeDirs(home); err != nil {
		t.Fatalf("EnsureHomeDirs: %v", err)
	}

	info, err := os.Stat(skillsDir)
	if err != nil {
		t.Fatalf("~/.nanite/skills/ not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("~/.nanite/skills/ exists but is not a directory")
	}
}

// TestEnsureHomeDirs_Idempotent verifies that calling EnsureHomeDirs twice
// does not return an error (MkdirAll is idempotent).
func TestEnsureHomeDirs_Idempotent(t *testing.T) {
	home := t.TempDir()

	if err := EnsureHomeDirs(home); err != nil {
		t.Fatalf("first EnsureHomeDirs: %v", err)
	}
	if err := EnsureHomeDirs(home); err != nil {
		t.Fatalf("second EnsureHomeDirs: %v", err)
	}
}

// TestWriteUserSkillFile_RejectsTraversalSlugs ensures that the dev-mode
// fork-to-user-override path cannot escape ~/.nanite/skills/ via crafted
// slug values (E1 hardening, post-PR-92 review).
func TestWriteUserSkillFile_RejectsTraversalSlugs(t *testing.T) {
	home := t.TempDir()
	cases := []string{
		"../escape",
		"../../etc/passwd",
		"sub/dir",
		`back\slash`,
		"..",
		"/abs/path",
		"weird..ok", // contains ".." — rejected even though no separator
	}
	for _, slug := range cases {
		if _, err := WriteUserSkillFile(home, slug, "body"); err == nil {
			t.Errorf("WriteUserSkillFile(%q) expected error, got nil", slug)
		}
	}
	// Sanity check: a clean slug still works.
	out, err := WriteUserSkillFile(home, "clean-slug", "body")
	if err != nil {
		t.Fatalf("WriteUserSkillFile(clean): %v", err)
	}
	expected := filepath.Join(home, ".nanite", "skills", "clean-slug.md")
	if out != expected {
		t.Errorf("path = %q, want %q", out, expected)
	}
}

// TASKS/skills/01: TestDropAndLoad_UserTierNoLongerDiscovered (TASKS/
// phase-1/08's negative verification that a file dropped into
// ~/.nanite/skills/ is not picked up by Discover()) is deleted along with
// Discover/DiscoverOptions themselves — see
// docs/engineering/architecture/20-skills.md's "Migration: clean slate, no
// carried-forward content" section. There is no more Discover() to call a
// negative check against; EnsureHomeDirs (exercised by the two tests above)
// is unaffected and stays.
