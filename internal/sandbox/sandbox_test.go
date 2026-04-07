package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestDir_CreatesDirectory(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	dir, err := Dir("test-session-123")
	if err != nil {
		t.Fatalf("Dir() error: %v", err)
	}

	expected := filepath.Join(tmpHome, baseDirName, "test-session-123")
	if dir != expected {
		t.Errorf("expected %s, got %s", expected, dir)
	}

	// Both the session dir and .sandbox/ subdir should exist.
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("session directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected a directory")
	}

	subDir := filepath.Join(dir, sandboxSubDir)
	info, err = os.Stat(subDir)
	if err != nil {
		t.Fatalf(".sandbox/ subdirectory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected .sandbox/ to be a directory")
	}
}

func TestDir_Idempotent(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	dir1, err := Dir("sess-abc")
	if err != nil {
		t.Fatalf("first Dir() error: %v", err)
	}
	dir2, err := Dir("sess-abc")
	if err != nil {
		t.Fatalf("second Dir() error: %v", err)
	}
	if dir1 != dir2 {
		t.Errorf("expected same path, got %s and %s", dir1, dir2)
	}
}

func TestPopulate_CreatesAllFiles(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, sandboxSubDir)
	os.MkdirAll(subDir, 0755)

	agent := &store.AgentProfile{
		ID:          "test-001",
		Name:        "Test Agent",
		Description: "A test agent for unit testing.",
		CanExecute:  true,
		MCPServers:  `["engine","conduit"]`,
	}
	mode := &store.AgentMode{
		Name:           "architect",
		PromptAddendum: "Focus on system design and architecture.",
	}

	if err := Populate(dir, agent, mode, PopulateOpts{SessionID: "test-sess", DBPath: "/tmp/test.db"}); err != nil {
		t.Fatalf("Populate() error: %v", err)
	}

	// Verify CLAUDE.md exists and is compact.
	claudeMD := readFile(t, filepath.Join(dir, "CLAUDE.md"))
	assertContains(t, claudeMD, "# Nanite Agent — Test Agent", "agent name header")
	assertContains(t, claudeMD, "A test agent for unit testing.", "agent description")
	assertContains(t, claudeMD, ".sandbox/envelope-schema.md", "pointer to envelope schema")
	assertContains(t, claudeMD, ".sandbox/agent-context.md", "pointer to agent context")
	assertContains(t, claudeMD, "ALWAYS set \"version\": 1", "envelope version rule")
	assertContains(t, claudeMD, "silently dropped", "unregistered type warning")
	// CLAUDE.md should NOT contain the full schema details.
	if strings.Contains(claudeMD, "### Question Object") {
		t.Error("CLAUDE.md should not contain full schema — that belongs in .sandbox/")
	}

	// Verify .sandbox/envelope-schema.md exists with full spec.
	envelopeMD := readFile(t, filepath.Join(subDir, "envelope-schema.md"))
	assertContains(t, envelopeMD, "# Nanite Envelope Schema", "schema header")
	assertContains(t, envelopeMD, "### Question Object", "question field reference")
	assertContains(t, envelopeMD, "### Proposal Object", "proposal field reference")
	assertContains(t, envelopeMD, "### Approval Object", "approval field reference")
	assertContains(t, envelopeMD, "ticket-confirmation", "registered type")
	assertContains(t, envelopeMD, "nanite-envelope", "code fence tag")

	// Verify .sandbox/agent-context.md exists with agent details.
	agentMD := readFile(t, filepath.Join(subDir, "agent-context.md"))
	assertContains(t, agentMD, "# Agent: Test Agent", "agent header")
	assertContains(t, agentMD, "**ID:** test-001", "agent ID")
	assertContains(t, agentMD, "**Can Execute:** true", "can_execute flag")
	assertContains(t, agentMD, "## Current Mode: architect", "mode name")
	assertContains(t, agentMD, "Focus on system design", "mode addendum")
	assertContains(t, agentMD, "- engine", "MCP server")
	assertContains(t, agentMD, "- conduit", "MCP server")

	// Verify .mcp.json exists with correct structure.
	mcpJSON := readFile(t, filepath.Join(dir, ".mcp.json"))
	assertContains(t, mcpJSON, `"mcpServers"`, "mcpServers key")
	assertContains(t, mcpJSON, `"nanite"`, "nanite server entry")
	assertContains(t, mcpJSON, `"mcp"`, "mcp subcommand in args")
	assertContains(t, mcpJSON, `"/tmp/test.db"`, "db path in args")
	assertContains(t, mcpJSON, `"test-sess"`, "session ID in args")
}

func TestPopulate_EmptyDescription(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, sandboxSubDir), 0755)

	agent := &store.AgentProfile{
		Name:        "Minimal Agent",
		Description: "",
		MCPServers:  "[]",
	}

	if err := Populate(dir, agent, &store.AgentMode{}, PopulateOpts{}); err != nil {
		t.Fatalf("Populate() error: %v", err)
	}

	claudeMD := readFile(t, filepath.Join(dir, "CLAUDE.md"))
	assertContains(t, claudeMD, "# Nanite Agent — Minimal Agent", "agent name")
	// Should not have triple newline from empty description.
	if strings.Contains(claudeMD, "\n\n\n") {
		t.Error("unexpected triple newline from empty description")
	}
}

func TestPopulate_NoMCPJsonWithoutDBPath(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, sandboxSubDir), 0755)

	agent := &store.AgentProfile{
		Name:       "Agent",
		MCPServers: "[]",
	}

	if err := Populate(dir, agent, &store.AgentMode{}, PopulateOpts{}); err != nil {
		t.Fatalf("Populate() error: %v", err)
	}

	// .mcp.json should NOT exist when DBPath is empty.
	if _, err := os.Stat(filepath.Join(dir, ".mcp.json")); err == nil {
		t.Error("expected no .mcp.json when DBPath is empty")
	}
}

func TestPopulate_NilMode(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, sandboxSubDir), 0755)

	agent := &store.AgentProfile{
		ID:         "test-002",
		Name:       "No Mode Agent",
		MCPServers: "[]",
	}

	if err := Populate(dir, agent, nil, PopulateOpts{}); err != nil {
		t.Fatalf("Populate() error: %v", err)
	}

	agentMD := readFile(t, filepath.Join(dir, sandboxSubDir, "agent-context.md"))
	if strings.Contains(agentMD, "## Current Mode") {
		t.Error("should not include mode section when mode is nil")
	}
}

// --- helpers ---

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func assertContains(t *testing.T, text, substr, label string) {
	t.Helper()
	if !strings.Contains(text, substr) {
		t.Errorf("missing %s: expected to find %q", label, substr)
	}
}
