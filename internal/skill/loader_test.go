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

// TestDropAndLoad_EndToEnd is the primary acceptance test for J6.
// It simulates the folder-drop flow:
//
//  1. EnsureHomeDirs creates ~/.nanite/skills/ if absent.
//  2. A skill file is dropped into the user-level directory.
//  3. Discover loads the file and returns the parsed definition.
//
// This test does not interact with the real home directory — HomeDir is
// overridden via DiscoverOptions to isolate from the developer's machine.
func TestDropAndLoad_EndToEnd(t *testing.T) {
	home := t.TempDir()

	// Step 1: ensure dirs (mirrors what NewContainer calls at startup).
	if err := EnsureHomeDirs(home); err != nil {
		t.Fatalf("EnsureHomeDirs: %v", err)
	}

	// Step 2: drop a skill file.
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

	// Step 3: discover — HomeDir set to temp home to isolate from real machine.
	defs, err := Discover(DiscoverOptions{HomeDir: home})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if len(defs) != 1 {
		t.Fatalf("got %d defs, want 1", len(defs))
	}
	got := defs[0]
	if got.Slug != "summarise" {
		t.Errorf("slug = %q, want %q", got.Slug, "summarise")
	}
	if got.Name != "Summarise" {
		t.Errorf("name = %q, want %q", got.Name, "Summarise")
	}
	if got.Source != "user" {
		t.Errorf("source = %q, want %q", got.Source, "user")
	}
}
