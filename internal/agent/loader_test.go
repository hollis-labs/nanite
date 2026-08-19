package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEnsureHomeDirs_CreatesDir verifies that EnsureHomeDirs creates
// ~/.nanite/agents/ when it does not yet exist.
func TestEnsureHomeDirs_CreatesDir(t *testing.T) {
	home := t.TempDir()
	agentsDir := filepath.Join(home, ".nanite", "agents")

	// Precondition: directory must not exist.
	if _, err := os.Stat(agentsDir); err == nil {
		t.Fatal("agents dir already exists before EnsureHomeDirs — test setup error")
	}

	if err := EnsureHomeDirs(home); err != nil {
		t.Fatalf("EnsureHomeDirs: %v", err)
	}

	info, err := os.Stat(agentsDir)
	if err != nil {
		t.Fatalf("~/.nanite/agents/ not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("~/.nanite/agents/ exists but is not a directory")
	}
}

// TestEnsureHomeDirs_Idempotent verifies that calling EnsureHomeDirs twice
// does not return an error.
func TestEnsureHomeDirs_Idempotent(t *testing.T) {
	home := t.TempDir()

	if err := EnsureHomeDirs(home); err != nil {
		t.Fatalf("first EnsureHomeDirs: %v", err)
	}
	if err := EnsureHomeDirs(home); err != nil {
		t.Fatalf("second EnsureHomeDirs: %v", err)
	}
}

// TestDropAndLoad_ProjectTierNoLongerDiscovered is TASKS/phase-1/08's
// negative verification for the .nanite/agents/ (project) tier, superseding
// the old end-to-end acceptance test of the same drop flow (which asserted
// the file WAS discovered — exactly the behavior this task cuts).
// EnsureHomeDirs still creates the directory on first run (unaffected,
// still exercised here to prove the two behaviors are independent), but a
// file dropped into it is no longer picked up by Discover() — the project
// directory-scan tier was removed in full.
func TestDropAndLoad_ProjectTierNoLongerDiscovered(t *testing.T) {
	root := t.TempDir()

	// Step 1: ensure dirs (mirrors what NewContainer calls at startup).
	if err := EnsureHomeDirs(root); err != nil {
		t.Fatalf("EnsureHomeDirs: %v", err)
	}

	// Step 2: drop an agent file into the project-level agents/ directory,
	// same as the old end-to-end flow.
	agentsDir := filepath.Join(root, ".nanite", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("mkdir agents: %v", err)
	}
	content := `---
name: Researcher
slug: researcher
description: Deep-research agent
---
You are a research assistant. Investigate topics thoroughly.
`
	if err := os.WriteFile(filepath.Join(agentsDir, "researcher.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write agent file: %v", err)
	}

	// Step 3: discover — must find nothing; the project tier is cut in full.
	defs, err := Discover(DiscoverOptions{WorkingDir: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(defs) != 0 {
		t.Fatalf("got %d defs, want 0 (project agent tier is cut)", len(defs))
	}
}
