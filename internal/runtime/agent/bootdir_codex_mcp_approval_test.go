package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestCodexLayout_PlantsApprovalForNaniteMCP(t *testing.T) {
	bootDir, err := codexLayout{}.Setup(SetupParams{
		SessionID: "sess-zz", RunID: "r0", AgentProfile: &store.AgentProfile{ID: "p", Name: "N", Slug: "n"},
		Mode: ModeOneShot, SystemPrompt: "x", BootContent: "b",
		MCPConfig: MCPConfig{BinaryPath: "/var/lib/fabric/bin/nanite", DBPath: "/tmp/db", APIBaseURL: "http://127.0.0.1:8090"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(bootDir)
	b, _ := os.ReadFile(filepath.Join(bootDir, "config.toml"))
	if !strings.Contains(string(b), "[mcp_servers.nanite]\ndefault_tools_approval_mode = \"approve\"\ncommand") {
		t.Fatalf("approval mode not planted:\n%s", b)
	}
}
