package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// testDirAdapter is a simple CLIAgentAdapter that discovers agents from a
// specific subdirectory, used to replace the old hardcoded .agentrc/.claude tiers in tests.
type testDirAdapter struct {
	name     string
	subDir   string
	source   string
	priority int
}

func (a *testDirAdapter) Name() string  { return a.name }
func (a *testDirAdapter) Priority() int { return a.priority }

func (a *testDirAdapter) Discover(projectDir string) ([]Definition, error) {
	dir := filepath.Join(projectDir, a.subDir)
	defs := discoverDir(dir, a.source)
	out := make([]Definition, len(defs))
	for i, d := range defs {
		out[i] = *d
	}
	return out, nil
}

func (a *testDirAdapter) PopulateSandbox(_ string, _ store.AgentProfile, _ SandboxContext) error {
	return nil
}

func (a *testDirAdapter) SyncProjectRoot(_ string, _ []store.AgentProfile) error {
	return nil
}

// newTestAdapterRegistry builds an AdapterRegistry with adapters for
// .agentrc/agents/ and .claude/agents/ (mirroring the real nanite-native and claude adapters).
func newTestAdapterRegistry() *AdapterRegistry {
	r := NewAdapterRegistry()
	r.Register(&testDirAdapter{name: "agentrc", subDir: filepath.Join(".agentrc", "agents"), source: "agentrc", priority: 50})
	r.Register(&testDirAdapter{name: "claude", subDir: filepath.Join(".claude", "agents"), source: "claude", priority: 60})
	return r
}

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

func TestDiscover_AdapterPriorityOrder(t *testing.T) {
	root := t.TempDir()

	// Write a unique agent via an adapter tier.
	writeAgentFile(t, filepath.Join(root, ".agentrc", "agents"), "research.md", "research")

	defs, err := Discover(DiscoverOptions{WorkingDir: root, Adapters: newTestAdapterRegistry()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(defs) != 1 {
		t.Fatalf("got %d definitions, want 1", len(defs))
	}
	if defs[0].Slug != "research" || defs[0].Source != "agentrc" {
		t.Errorf("defs[0]: slug=%q source=%q, want research/agentrc", defs[0].Slug, defs[0].Source)
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

// TestDiscover_CLIAgentWinsOverAdapter proves the CLI single-file tier still
// takes priority over an adapter-discovered definition with the same slug —
// the CLI tier's priority-1 position is unchanged by this task, only the
// removed project/user/plugin directory-scan tiers that used to sit below it
// are gone.
func TestDiscover_CLIAgentWinsOverAdapter(t *testing.T) {
	root := t.TempDir()

	cliPath := filepath.Join(root, "custom.md")
	content := "---\nname: Custom\nslug: custom\n---\nCLI agent.\n"
	if err := os.WriteFile(cliPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	writeAgentFile(t, filepath.Join(root, ".agentrc", "agents"), "custom.md", "custom")

	defs, err := Discover(DiscoverOptions{
		CLIAgentPath: cliPath,
		WorkingDir:   root,
		Adapters:     newTestAdapterRegistry(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(defs) != 1 {
		t.Fatalf("got %d definitions, want 1 (dedup)", len(defs))
	}
	if defs[0].Source != "cli" {
		t.Errorf("source = %q, want %q (CLI tier is highest priority)", defs[0].Source, "cli")
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

func TestDiscover_ClaudeCodeAgents(t *testing.T) {
	root := t.TempDir()
	writeAgentFile(t, filepath.Join(root, ".claude", "agents"), "helper.md", "helper")

	defs, err := Discover(DiscoverOptions{WorkingDir: root, Adapters: newTestAdapterRegistry()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(defs) != 1 || defs[0].Source != "claude" {
		t.Errorf("got %v, want 1 agent with source=claude", defs)
	}
}

func TestDiscover_SlugDedup(t *testing.T) {
	root := t.TempDir()

	// Same slug across both remaining adapter-discovered sources.
	writeAgentFile(t, filepath.Join(root, ".agentrc", "agents"), "dupe.md", "dupe")
	writeAgentFile(t, filepath.Join(root, ".claude", "agents"), "dupe.md", "dupe")

	defs, err := Discover(DiscoverOptions{
		WorkingDir: root,
		Adapters:   newTestAdapterRegistry(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(defs) != 1 {
		t.Fatalf("got %d definitions, want 1 (dedup)", len(defs))
	}
	if defs[0].Source != "agentrc" {
		t.Errorf("source = %q, want %q (higher-priority adapter)", defs[0].Source, "agentrc")
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

func TestDiscover_SkipsInvalidFiles(t *testing.T) {
	// discoverDir itself (still used by adapter-based discovery, see
	// testDirAdapter above) must still skip unparseable/non-.md files —
	// this behavior is unchanged by the tier removal, only which tiers call
	// discoverDir changed.
	root := t.TempDir()
	dir := filepath.Join(root, ".agentrc", "agents")
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

	defs, err := Discover(DiscoverOptions{WorkingDir: root, Adapters: newTestAdapterRegistry()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(defs) != 1 || defs[0].Slug != "good" {
		t.Errorf("got %v, want 1 agent with slug=good", defs)
	}
}
