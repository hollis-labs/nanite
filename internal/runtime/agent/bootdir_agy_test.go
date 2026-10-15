package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestAgyLayoutPlantsTheWorkspacePlugin(t *testing.T) {
	params := SetupParams{
		SessionID:    "agy-layout",
		AgentProfile: &store.AgentProfile{Name: "AGY Test", Slug: "agy-test", Description: "d"},
		SystemPrompt: "Be brief.",
		BootContent:  "kickoff",
		MCPConfig:    MCPConfig{BinaryPath: "/bin/nanite", DBPath: "/tmp/x.db", APIBaseURL: "http://127.0.0.1:1", AgentCredential: func(string) (string, error) { return "cred-for-test", nil }},
	}
	layout := bootdirLayoutFor("agy")
	if _, ok := layout.(agyLayout); !ok {
		t.Fatalf("bootdirLayoutFor(agy) = %T", layout)
	}
	boot, err := layout.Setup(params)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(boot) })

	read := func(rel string) string {
		b, readErr := os.ReadFile(filepath.Join(boot, rel)) //nolint:gosec // G304: boot is the test's own planted temp dir
		if readErr != nil {
			t.Fatalf("%s: %v", rel, readErr)
		}
		return string(b)
	}
	if got := read("AGENTS.md"); !strings.Contains(got, "Be brief.") {
		t.Errorf("AGENTS.md lacks the system prompt: %q", got)
	}
	if read("boot.md") != "kickoff" {
		t.Error("boot.md not planted")
	}
	if got := read(".agents/plugins/tether/plugin.json"); !strings.Contains(got, `"tether"`) {
		t.Errorf("plugin.json = %q", got)
	}
	mcp := read(agyMCPConfigPath)
	if !strings.Contains(mcp, "mcpServers") || !strings.Contains(mcp, "/bin/nanite") {
		t.Errorf("mcp_config.json = %q", mcp)
	}
	info, err := os.Stat(filepath.Join(boot, agyMCPConfigPath))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("mcp_config.json mode = %v, %v; it carries a credential and must be 0600", info.Mode().Perm(), err)
	}
	if _, err := os.Stat(filepath.Join(boot, ".mcp.json")); err == nil {
		t.Error("agy does not read .mcp.json; it must not be planted")
	}
	if got := layout.SpawnWorkdir(boot, "/project"); got != boot {
		t.Errorf("SpawnWorkdir = %q, want the boot dir", got)
	}
	if env := layout.AmendEnv(map[string]string{"A": "b"}, boot); len(env) != 1 {
		t.Errorf("AmendEnv changed the env: %v", env)
	}
}
