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

// TestDropAndLoad_UserTierNoLongerDiscovered is TASKS/phase-1/08's negative
// verification for the ~/.nanite/skills/ (user) tier, superseding the old
// J6 acceptance test of the same drop flow (which asserted the file WAS
// discovered — exactly the behavior this task cuts). EnsureHomeDirs still
// creates the directory on first run (unaffected, still exercised here to
// prove the two behaviors are independent), but a file dropped into it is
// no longer picked up by Discover() — DiscoverOptions doesn't even carry a
// HomeDir field to point at it anymore.
func TestDropAndLoad_UserTierNoLongerDiscovered(t *testing.T) {
	home := t.TempDir()

	// Step 1: ensure dirs (mirrors what NewContainer calls at startup).
	if err := EnsureHomeDirs(home); err != nil {
		t.Fatalf("EnsureHomeDirs: %v", err)
	}

	// Step 2: drop a skill file, same as the old J6 flow.
	skillsDir := filepath.Join(home, ".nanite", "skills")
	content := `---
name: Summarise
slug: summarise
description: Summarise the current conversation
---
Summarise everything discussed so far in three bullet points.
`
	if err := os.WriteFile(filepath.Join(skillsDir, "summarise.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}

	// Step 3: discover — must find nothing; the user tier is cut in full.
	defs, err := Discover(DiscoverOptions{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(defs) != 0 {
		t.Fatalf("got %d defs, want 0 (user skill tier is cut)", len(defs))
	}
}
