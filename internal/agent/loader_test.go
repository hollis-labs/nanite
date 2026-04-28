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

// TestDropAndLoad_EndToEnd verifies the folder-drop flow for agents:
//
//  1. EnsureHomeDirs creates ~/.nanite/agents/ if absent.
//  2. An agent file is dropped into the user-level directory.
//  3. Discover loads the file and returns the parsed definition.
//
// HomeDir is not a DiscoverOptions field in agent.Discover — the function
// calls os.UserHomeDir() directly. This test therefore passes the agent file
// via the WorkingDir path (.nanite/agents/) which maps to the project tier.
// The user-tier discovery path is exercised by TestEnsureHomeDirs_CreatesDir
// + the real container startup call; see loader.go comment for J7 handoff.
func TestDropAndLoad_EndToEnd(t *testing.T) {
	root := t.TempDir()

	// Step 1: ensure dirs (mirrors what NewContainer calls at startup).
	if err := EnsureHomeDirs(root); err != nil {
		t.Fatalf("EnsureHomeDirs: %v", err)
	}

	// Step 2: drop an agent file into the project-level agents/ directory.
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

	// Step 3: discover — WorkingDir set to temp root.
	defs, err := Discover(DiscoverOptions{WorkingDir: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if len(defs) != 1 {
		t.Fatalf("got %d defs, want 1", len(defs))
	}
	got := defs[0]
	if got.Slug != "researcher" {
		t.Errorf("slug = %q, want %q", got.Slug, "researcher")
	}
	if got.Name != "Researcher" {
		t.Errorf("name = %q, want %q", got.Name, "Researcher")
	}
	if got.Source != "project" {
		t.Errorf("source = %q, want %q", got.Source, "project")
	}
}
