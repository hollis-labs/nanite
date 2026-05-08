package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestClaudeLayout_Setup_FileShape verifies every planted file lands in the
// expected location with the expected content.
func TestClaudeLayout_Setup_FileShape(t *testing.T) {
	profile := &store.AgentProfile{
		ID:           "agent-test",
		Name:         "Test Agent",
		Slug:         "test-agent",
		Description:  "A test agent for bootdir verification",
		DefaultModel: "claude-sonnet-4-6",
	}

	bootDir, err := claudeLayout{}.Setup(SetupParams{
		SessionID:    "sess-1",
		RunID:        "run-7",
		AgentProfile: profile,
		Mode:         ModeLongLived,
		SystemPrompt: "You are a test agent.",
		BootContent:  "# Boot\n\nrole: tester\n",
		ProjectDir:   t.TempDir(),
		MCPConfig: MCPConfig{
			BinaryPath: "/usr/local/bin/nanite-test",
			DBPath:     "/tmp/test.db",
			ServerID:   "nanite-test",
		},
	})
	if err != nil {
		t.Fatalf("claudeLayout.Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(bootDir) })

	if !strings.Contains(filepath.Base(bootDir), "nanite-boot-claude-sess-1-r") {
		t.Errorf("boot dir name %q missing forensic prefix", filepath.Base(bootDir))
	}

	cases := []struct {
		path        string
		mustContain string
	}{
		{"CLAUDE.md", "Test Agent"},
		{"CLAUDE.md", "test agent for bootdir verification"},
		{"CLAUDE.md", "## Envelope Format"},
		{"boot.md", "role: tester"},
		{".sandbox/agent-context.md", "Test Agent"},
		{".sandbox/envelope-schema.md", "Nanite Envelope Schema"},
		{".claude/settings.json", "mcpServers"},
		{".mcp.json", "/usr/local/bin/nanite-test"},
		{".mcp.json", "sess-1"},
		{".mcp.json", "nanite-test"},
	}
	for _, c := range cases {
		body, err := os.ReadFile(filepath.Join(bootDir, c.path))
		if err != nil {
			t.Errorf("missing %s: %v", c.path, err)
			continue
		}
		if !strings.Contains(string(body), c.mustContain) {
			t.Errorf("%s missing %q\n--- body ---\n%s\n", c.path, c.mustContain, string(body))
		}
	}
}

// TestClaudeLayout_Setup_NoMCPWhenDBPathEmpty verifies .mcp.json is skipped
// when DBPath is empty (matches existing adapter-claude behavior).
func TestClaudeLayout_Setup_NoMCPWhenDBPathEmpty(t *testing.T) {
	profile := &store.AgentProfile{Name: "no-mcp", Slug: "no-mcp"}
	bootDir, err := claudeLayout{}.Setup(SetupParams{
		SessionID:    "s1",
		AgentProfile: profile,
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(bootDir) })

	if _, err := os.Stat(filepath.Join(bootDir, ".mcp.json")); !os.IsNotExist(err) {
		t.Errorf(".mcp.json should not exist when MCPConfig.DBPath is empty")
	}
}

// TestClaudeLayout_Setup_RequiresAgentProfile guards against nil profile
// crashes — Boot may pass a freshly-resolved profile that is nil if the
// AgentProfiles dependency mis-implements GetOrDefault.
func TestClaudeLayout_Setup_RequiresAgentProfile(t *testing.T) {
	_, err := claudeLayout{}.Setup(SetupParams{SessionID: "s1"})
	if err == nil || !strings.Contains(err.Error(), "AgentProfile") {
		t.Fatalf("expected AgentProfile-required error, got %v", err)
	}
}

// TestClaudeLayout_SettingsJSON_Stub verifies the stub keeps mcpServers
// and approvedTools empty so no global ~/.claude.json bleed reaches the
// child.
func TestClaudeLayout_SettingsJSON_Stub(t *testing.T) {
	profile := &store.AgentProfile{Name: "settings", Slug: "settings"}
	bootDir, err := claudeLayout{}.Setup(SetupParams{SessionID: "s1", AgentProfile: profile})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(bootDir) })

	body, err := os.ReadFile(filepath.Join(bootDir, ".claude/settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var parsed struct {
		MCPServers    map[string]any `json:"mcpServers"`
		ApprovedTools []any          `json:"approvedTools"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("parse settings.json: %v", err)
	}
	if len(parsed.MCPServers) != 0 {
		t.Errorf("mcpServers should be empty, got %v", parsed.MCPServers)
	}
	if len(parsed.ApprovedTools) != 0 {
		t.Errorf("approvedTools should be empty, got %v", parsed.ApprovedTools)
	}
}

// TestClaudeLayout_BootProperties confirms the layout's static metadata
// matches the long-lived PTY contract.
func TestClaudeLayout_BootProperties(t *testing.T) {
	l := claudeLayout{}
	if l.BootMode() != "stdin" {
		t.Errorf("BootMode = %q, want stdin", l.BootMode())
	}
	if got := l.SpawnWorkdir("/tmp/boot", "/proj"); got != "/tmp/boot" {
		t.Errorf("SpawnWorkdir = %q, want /tmp/boot", got)
	}
	prompt := l.BootPrompt(&store.AgentProfile{Name: "x"}, Options{Role: "executor"})
	if prompt == "" {
		t.Errorf("BootPrompt should compose a non-empty system prompt")
	}
}
