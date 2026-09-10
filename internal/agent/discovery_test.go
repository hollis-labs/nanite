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

func TestDiscover_CLIAgent(t *testing.T) {
	root := t.TempDir()

	// Write a CLI agent file.
	cliPath := filepath.Join(root, "custom.md")
	content := "---\nname: Custom\nslug: custom\n---\nCLI agent.\n"
	if err := os.WriteFile(cliPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

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

// TestDiscover_AdapterTierRemoved is CW-20260910-0012's negative
// verification, the counterpart to TestDiscover_ProjectUserPluginTiersRemoved
// below.
//
// The adapter tier survived TASKS/phase-0/16 and phase-2/06 as an
// always-empty loop, kept as a live extension point. Now that
// CLIAgentAdapter.Discover has become Import(path) -- an explicit,
// operator-initiated read -- that loop would have handed every adapter a
// working directory nobody named, on every boot: the directory-scan tier
// phase-1/08 removed, rebuilt under a new method name. So the loop is gone,
// along with DiscoverOptions.Adapters.
//
// Real subagent files in the real location, and Discover finds nothing.
// adapter-claude WOULD import these if an operator named the path
// (see its import_test.go) -- which is the entire distinction.
func TestDiscover_AdapterTierRemoved(t *testing.T) {
	root := t.TempDir()
	writeAgentFile(t, filepath.Join(root, ".claude", "agents"), "helper.md", "helper")
	writeAgentFile(t, filepath.Join(root, ".agentrc", "agents"), "research.md", "research")

	defs, err := Discover(DiscoverOptions{WorkingDir: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(defs) != 0 {
		t.Fatalf("got %d definitions, want 0 — boot must not read agent files from a directory nobody named", len(defs))
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

// TestDiscover_ProjectUserPluginTiersRemoved is TASKS/phase-1/08's negative
// verification: a file dropped into .nanite/agents/ (project),
// ~/.nanite/agents/ (user), or plugins/*/agents/ (plugin) before Discover()
// runs must NOT be picked up. These three directory-scan tiers, and
// discoverPluginAgents, were removed in full — not merely stopped-calling
// with empty args.
func TestDiscover_ProjectUserPluginTiersRemoved(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	pluginsDir := filepath.Join(root, "plugins")

	writeAgentFile(t, filepath.Join(root, ".nanite", "agents"), "project-agent.md", "project-agent")
	writeAgentFile(t, filepath.Join(home, ".nanite", "agents"), "user-agent.md", "user-agent")
	writeAgentFile(t, filepath.Join(pluginsDir, "example-plugin", "agents"), "plugin-agent.md", "plugin-agent")

	// DiscoverOptions no longer carries HomeDir/PluginsDir fields at all
	// (removed alongside the tiers that read them) — WorkingDir is the only
	// field left that could theoretically point at project-agent.md, and
	// even it is not read by any directory-scan tier anymore.
	defs, err := Discover(DiscoverOptions{WorkingDir: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(defs) != 0 {
		t.Fatalf("got %d definitions, want 0 (project/user/plugin tiers are cut)", len(defs))
	}
}
