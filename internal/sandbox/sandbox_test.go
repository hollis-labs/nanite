package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	agentpkg "github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/pathsafe"
	adapterclaude "github.com/hollis-labs/nanite/internal/plugin/builtin/adapter-claude"
	"github.com/hollis-labs/nanite/internal/store"
	"strings"
)

func TestDir_CreatesDirectory(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	dir, err := Dir("test-session-123")
	if err != nil {
		t.Fatalf("Dir() error: %v", err)
	}

	expected := filepath.Join(tmpHome, baseDirName, "test-session-123")
	// pathsafe.ResolveUnder normalizes through filepath.EvalSymlinks, which
	// on macOS resolves /var → /private/var. Accept either form.
	if dir != expected && !strings.HasSuffix(dir, filepath.Join(baseDirName, "test-session-123")) {
		t.Errorf("expected %s (or symlink-resolved equivalent), got %s", expected, dir)
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

// TestDir_RejectsTraversal verifies that a session ID containing path
// traversal segments never escapes the sandbox base dir. Before the fix,
// `filepath.Join(home, baseDirName, "../../etc")` silently collapsed to
// `/etc`, and MkdirAll happily tried to create it. The regex guard now
// refuses the value before any path resolution happens, and pathsafe
// provides the second-layer containment check.
func TestDir_RejectsTraversal(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	traversals := []string{
		"../../etc",
		"..",
		"foo/../bar",
		"../escape",
		"/absolute/escape",
		"", // empty — no default allowed
	}
	for _, sid := range traversals {
		t.Run(sid, func(t *testing.T) {
			_, err := Dir(sid)
			if err == nil {
				t.Fatalf("Dir(%q) returned no error; expected rejection", sid)
			}
		})
	}
}

// TestDir_RejectsSeatbeltInjectionChars verifies that bytes with meaning to
// sandbox-exec's TinyScheme parser (quotes, parens, semicolons,
// backslashes, control chars) are refused at the session ID gate — so they
// can never reach the profile literal on darwin.
func TestDir_RejectsSeatbeltInjectionChars(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	hostile := []string{
		`foo") (allow file-write*) ;"`,
		`a"b`,
		`a(b`,
		`a)b`,
		`a;b`,
		"a\x00b",
		"a\x1fb",
		"a b", // whitespace
	}
	for _, sid := range hostile {
		t.Run(sid, func(t *testing.T) {
			_, err := Dir(sid)
			if err == nil {
				t.Fatalf("Dir(%q) accepted hostile session id", sid)
			}
		})
	}
}

// TestDir_AcceptsValidSessionIDs confirms that legitimate slugs, UUIDs,
// and short test names pass the regex gate.
func TestDir_AcceptsValidSessionIDs(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	valid := []string{
		"sess-abc",
		"01234567-89ab-cdef-0123-456789abcdef",
		"CamelCase_123",
		"a",
	}
	for _, sid := range valid {
		if _, err := Dir(sid); err != nil {
			t.Errorf("Dir(%q) rejected valid id: %v", sid, err)
		}
	}
}

// TestDir_EscapeErrorIsTyped exercises the pathsafe containment layer. The
// regex gate blocks most escape vectors; this test injects a crafted value
// that the gate would pass but pathsafe must still catch. The current gate
// is strict enough that no such value exists in practice — the test
// instead asserts the error chain so downstream code (telemetry, logs)
// can rely on the typed error when the gate evolves.
func TestDir_EscapeErrorTypeAvailable(t *testing.T) {
	// Direct pathsafe invocation with a crafted input that escapes root.
	root := t.TempDir()
	_, err := pathsafe.ResolveUnder(root, "../escape")
	if err == nil {
		t.Fatal("pathsafe.ResolveUnder accepted a traversal")
	}
	var esc *pathsafe.EscapeError
	if !errors.As(err, &esc) {
		t.Fatalf("expected *pathsafe.EscapeError, got %T: %v", err, err)
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

func newTestRegistry() *agentpkg.AdapterRegistry {
	reg := agentpkg.NewAdapterRegistry()
	p := adapterclaude.New()
	reg.Register(p.Adapter())
	return reg
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

	if err := Populate(dir, agent, nil, PopulateOpts{
		SessionID: "test-sess",
		DBPath:    "/tmp/test.db",
		Adapters:  newTestRegistry(),
	}); err != nil {
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

	if err := Populate(dir, agent, nil, PopulateOpts{Adapters: newTestRegistry()}); err != nil {
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

	if err := Populate(dir, agent, nil, PopulateOpts{Adapters: newTestRegistry()}); err != nil {
		t.Fatalf("Populate() error: %v", err)
	}

	// .mcp.json should NOT exist when DBPath is empty.
	if _, err := os.Stat(filepath.Join(dir, ".mcp.json")); err == nil {
		t.Error("expected no .mcp.json when DBPath is empty")
	}
}

func TestPopulate_NilAdapters(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, sandboxSubDir), 0755)

	agent := &store.AgentProfile{
		ID:         "test-002",
		Name:       "No Adapter Agent",
		MCPServers: "[]",
	}

	// With nil Adapters, Populate should succeed (no-op beyond dir creation).
	if err := Populate(dir, agent, nil, PopulateOpts{}); err != nil {
		t.Fatalf("Populate() error: %v", err)
	}

	// No CLAUDE.md should be written when no adapters are registered.
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err == nil {
		t.Error("expected no CLAUDE.md when Adapters is nil")
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
