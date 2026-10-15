package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// Codex reads MCP servers from config.toml, not from the .mcp.json sidecar, so
// the Nanite MCP server must be planted there, in an owner-only file because its
// environment carries the session credential.
func TestCodexLayoutPlantsTheNaniteMCPServerInConfigTOML(t *testing.T) {
	params := SetupParams{
		SessionID:    "codex-mcp",
		AgentProfile: &store.AgentProfile{Name: "Codex MCP", Slug: "codex-mcp"},
		SystemPrompt: "x",
		MCPConfig:    MCPConfig{BinaryPath: "/bin/nanite", DBPath: "/tmp/x.db", APIBaseURL: "http://127.0.0.1:1", AgentCredential: func(string) (string, error) { return "cred-for-test", nil }},
	}
	boot, err := bootdirLayoutFor("codex").Setup(params)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(boot) })
	b, err := os.ReadFile(filepath.Join(boot, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	toml := string(b)
	for _, want := range []string{"[mcp_servers.", `command = "/bin/nanite"`, `"mcp"`, "NANITE_API_URL"} {
		if !strings.Contains(toml, want) {
			t.Errorf("config.toml lacks %q:\n%s", want, toml)
		}
	}
	if info, err := os.Stat(filepath.Join(boot, "config.toml")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("config.toml mode = %v, %v; it carries a credential and must be 0600", info.Mode().Perm(), err)
	}
}

func TestCodexLayoutPlantsNoMCPServerWhenDisabled(t *testing.T) {
	params := SetupParams{SessionID: "codex-nomcp", AgentProfile: &store.AgentProfile{Name: "c", Slug: "c"}, SystemPrompt: "x"}
	boot, err := bootdirLayoutFor("codex").Setup(params)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(boot) })
	b, _ := os.ReadFile(filepath.Join(boot, "config.toml"))
	if strings.Contains(string(b), "mcp_servers") {
		t.Errorf("MCP disabled, but config.toml names a server:\n%s", b)
	}
}
