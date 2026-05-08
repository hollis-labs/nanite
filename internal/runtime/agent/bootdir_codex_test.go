package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestCodexLayout_Setup_FileShape verifies AGENTS.md replaces CLAUDE.md
// and no .claude/ directory is planted.
func TestCodexLayout_Setup_FileShape(t *testing.T) {
	profile := &store.AgentProfile{
		ID:          "codex-agent",
		Name:        "Codex Test",
		Slug:        "codex-test",
		Description: "Codex bootdir verifier",
	}

	bootDir, err := codexLayout{}.Setup(SetupParams{
		SessionID:    "sess-c1",
		RunID:        "r0",
		AgentProfile: profile,
		Mode:         ModeOneShot,
		SystemPrompt: "You are codex test.",
		BootContent:  "# Boot\n",
		MCPConfig:    MCPConfig{BinaryPath: "/bin/nanite", DBPath: "/tmp/db"},
	})
	if err != nil {
		t.Fatalf("codexLayout.Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(bootDir) })

	if !strings.Contains(filepath.Base(bootDir), "nanite-boot-codex-sess-c1-r") {
		t.Errorf("boot dir name %q missing forensic prefix", filepath.Base(bootDir))
	}

	body, err := os.ReadFile(filepath.Join(bootDir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if !strings.Contains(string(body), "Codex Test") {
		t.Errorf("AGENTS.md missing agent name\n%s", string(body))
	}
	if !strings.Contains(string(body), "You are codex test.") {
		t.Errorf("AGENTS.md missing system prompt body\n%s", string(body))
	}

	if _, err := os.Stat(filepath.Join(bootDir, ".claude")); !os.IsNotExist(err) {
		t.Errorf("codex layout should not plant .claude/ directory")
	}
	if _, err := os.Stat(filepath.Join(bootDir, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Errorf("codex layout should not plant CLAUDE.md")
	}

	// Sandbox + boot.md + .mcp.json common to nanite layouts must exist.
	for _, p := range []string{"boot.md", ".sandbox/agent-context.md", ".sandbox/envelope-schema.md", ".mcp.json"} {
		if _, err := os.Stat(filepath.Join(bootDir, p)); err != nil {
			t.Errorf("missing common file %s: %v", p, err)
		}
	}
}

// TestCodexLayout_BootProperties confirms BootMode is empty (subprocess-
// per-turn delivery), SpawnWorkdir is the boot dir.
func TestCodexLayout_BootProperties(t *testing.T) {
	l := codexLayout{}
	if l.BootMode() != "" {
		t.Errorf("codex BootMode = %q, want empty", l.BootMode())
	}
	if got := l.SpawnWorkdir("/tmp/boot", "/proj"); got != "/tmp/boot" {
		t.Errorf("SpawnWorkdir = %q, want /tmp/boot", got)
	}
}
