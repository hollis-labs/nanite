package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func writeAgentFile(t *testing.T, dir, filename, slug string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + slug + "\nslug: " + slug + "\n---\nPrompt for " + slug + ".\n"
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscover_PriorityOrder(t *testing.T) {
	root := t.TempDir()

	// Write agents at two priority levels with the same slug.
	writeAgentFile(t, filepath.Join(root, ".nanite", "agents"), "code.md", "code")
	writeAgentFile(t, filepath.Join(root, ".agentrc", "agents"), "code.md", "code")

	// Write a unique agent at lower priority.
	writeAgentFile(t, filepath.Join(root, ".agentrc", "agents"), "research.md", "research")

	defs, err := Discover(DiscoverOptions{WorkingDir: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(defs) != 2 {
		t.Fatalf("got %d definitions, want 2", len(defs))
	}

	// "code" should come from project (.nanite/agents/), not agentrc.
	if defs[0].Slug != "code" || defs[0].Source != "project" {
		t.Errorf("defs[0]: slug=%q source=%q, want code/project", defs[0].Slug, defs[0].Source)
	}

	// "research" only exists in agentrc.
	if defs[1].Slug != "research" || defs[1].Source != "agentrc" {
		t.Errorf("defs[1]: slug=%q source=%q, want research/agentrc", defs[1].Slug, defs[1].Source)
	}
}

func TestDiscover_CLIAgent(t *testing.T) {
	root := t.TempDir()

	// Write a CLI agent file.
	cliPath := filepath.Join(root, "custom.md")
	content := "---\nname: Custom\nslug: custom\n---\nCLI agent.\n"
	if err := os.WriteFile(cliPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Also write a project agent with the same slug — CLI should win.
	writeAgentFile(t, filepath.Join(root, ".nanite", "agents"), "custom.md", "custom")

	defs, err := Discover(DiscoverOptions{
		CLIAgentPath: cliPath,
		WorkingDir:   root,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, d := range defs {
		if d.Slug == "custom" {
			found = true
			if d.Source != "cli" {
				t.Errorf("custom agent source = %q, want %q", d.Source, "cli")
			}
		}
	}
	if !found {
		t.Error("custom agent not found")
	}
}

func TestDiscover_PluginAgents(t *testing.T) {
	root := t.TempDir()
	pluginsDir := filepath.Join(root, "plugins")

	writeAgentFile(t, filepath.Join(pluginsDir, "support-ticket", "agents"), "support.md", "support")

	defs, err := Discover(DiscoverOptions{
		WorkingDir: root,
		PluginsDir: pluginsDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(defs) != 1 || defs[0].Slug != "support" || defs[0].Source != "plugin" {
		t.Errorf("got %v, want 1 agent with slug=support source=plugin", defs)
	}
}

func TestDiscover_MissingDirs(t *testing.T) {
	root := t.TempDir()

	// No agent directories exist — should return empty, no error.
	defs, err := Discover(DiscoverOptions{WorkingDir: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(defs) != 0 {
		t.Errorf("got %d definitions, want 0", len(defs))
	}
}

func TestDiscover_SkipsInvalidFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".nanite", "agents")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write a valid agent.
	writeAgentFile(t, dir, "good.md", "good")

	// Write an invalid file (no frontmatter).
	if err := os.WriteFile(filepath.Join(dir, "bad.md"), []byte("no frontmatter"), 0644); err != nil {
		t.Fatal(err)
	}

	// Write a non-md file (should be skipped).
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not an agent"), 0644); err != nil {
		t.Fatal(err)
	}

	defs, err := Discover(DiscoverOptions{WorkingDir: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(defs) != 1 || defs[0].Slug != "good" {
		t.Errorf("got %v, want 1 agent with slug=good", defs)
	}
}

func TestDiscover_ClaudeCodeAgents(t *testing.T) {
	root := t.TempDir()
	writeAgentFile(t, filepath.Join(root, ".claude", "agents"), "helper.md", "helper")

	defs, err := Discover(DiscoverOptions{WorkingDir: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(defs) != 1 || defs[0].Source != "claude" {
		t.Errorf("got %v, want 1 agent with source=claude", defs)
	}
}

func TestDiscover_SlugDedup(t *testing.T) {
	root := t.TempDir()

	// Same slug across all 4 project-local sources.
	writeAgentFile(t, filepath.Join(root, ".nanite", "agents"), "dupe.md", "dupe")
	writeAgentFile(t, filepath.Join(root, ".agentrc", "agents"), "dupe.md", "dupe")
	writeAgentFile(t, filepath.Join(root, ".claude", "agents"), "dupe.md", "dupe")

	pluginsDir := filepath.Join(root, "plugins")
	writeAgentFile(t, filepath.Join(pluginsDir, "myplugin", "agents"), "dupe.md", "dupe")

	defs, err := Discover(DiscoverOptions{
		WorkingDir: root,
		PluginsDir: pluginsDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(defs) != 1 {
		t.Fatalf("got %d definitions, want 1 (dedup)", len(defs))
	}
	if defs[0].Source != "project" {
		t.Errorf("source = %q, want %q (highest priority)", defs[0].Source, "project")
	}
}
