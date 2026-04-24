package toolclient

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/go-providers/provider"
)

// ---------------------------------------------------------------------------
// isDevTool unit tests
// ---------------------------------------------------------------------------

func TestIsDevTool(t *testing.T) {
	cases := []struct {
		name      string
		toolName  string
		wantIsDev bool
	}{
		// Bare dev-server names.
		{"dev_bash bare", "dev_bash", true},
		{"dev_read bare", "dev_read", true},
		{"dev_write bare", "dev_write", true},
		{"dev_edit bare", "dev_edit", true},
		{"dev_glob bare", "dev_glob", true},
		{"dev_grep bare", "dev_grep", true},
		// MCP-prefixed dev-server names.
		{"dev_bash prefixed", "mcp__dev__dev_bash", true},
		{"dev_read prefixed", "mcp__dev__dev_read", true},
		{"dev_write prefixed", "mcp__dev__dev_write", true},
		// Non-dev tools must not be flagged.
		{"general web_fetch", "web_fetch", false},
		{"mcp other server", "mcp__general__web_fetch", false},
		{"nanite self-tool", "nanite_todo_create", false},
		{"mcp self", "mcp__self__nanite_todo_create", false},
		// Edge cases.
		{"empty string", "", false},
		{"dev prefix no underscore", "dev", false},
		// A tool named "developer_mode" should NOT be flagged — it starts
		// with "dev" but not "dev_".
		{"developer_mode not dev server", "developer_mode", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isDevTool(c.toolName)
			if got != c.wantIsDev {
				t.Errorf("isDevTool(%q) = %v, want %v", c.toolName, got, c.wantIsDev)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// developerModeEnabled — DeveloperModeFunc injection
// ---------------------------------------------------------------------------

func TestDeveloperModeEnabled_FuncOverride(t *testing.T) {
	tb := New(nil, nil, DefaultConfig())

	// Without func and without store: defaults to false (fail-closed).
	if tb.developerModeEnabled() {
		t.Error("expected developerModeEnabled()=false when no func and no store")
	}

	// Inject func returning true.
	tb.DeveloperModeFunc = func() bool { return true }
	if !tb.developerModeEnabled() {
		t.Error("expected developerModeEnabled()=true when DeveloperModeFunc returns true")
	}

	// Inject func returning false.
	tb.DeveloperModeFunc = func() bool { return false }
	if tb.developerModeEnabled() {
		t.Error("expected developerModeEnabled()=false when DeveloperModeFunc returns false")
	}
}

// ---------------------------------------------------------------------------
// SelectToolsAsProvider — dev_mode gate at selection time
// ---------------------------------------------------------------------------

// TestSelectToolsAsProvider_DevToolsExcludedWhenDevModeOff verifies that
// dev_bash, dev_read, and other dev-server tools are stripped from the
// selection result when developer_mode is false.
func TestSelectToolsAsProvider_DevToolsExcludedWhenDevModeOff(t *testing.T) {
	tb := New(nil, nil, DefaultConfig())
	tb.DeveloperModeFunc = func() bool { return false }

	// Register a mix of dev tools and a safe tool as builtins.
	tb.Builtins.RegisterBuiltins("dev", []provider.ToolDefinition{
		{Name: "dev_bash", Description: "Execute shell commands"},
		{Name: "dev_read", Description: "Read files"},
		{Name: "dev_write", Description: "Write files"},
		{Name: "dev_edit", Description: "Edit files"},
		{Name: "dev_glob", Description: "Glob files"},
		{Name: "dev_grep", Description: "Grep files"},
	})
	tb.Builtins.RegisterBuiltins("self", []provider.ToolDefinition{
		{Name: "nanite_todo_create", Description: "Create a todo"},
	})

	res, err := tb.SelectToolsAsProvider(context.Background(), "general", nil, "", "agent-1")
	if err != nil {
		t.Fatalf("SelectToolsAsProvider: %v", err)
	}

	names := toolNames(res.Tools)
	devTools := []string{"dev_bash", "dev_read", "dev_write", "dev_edit", "dev_glob", "dev_grep"}
	for _, devTool := range devTools {
		if names[devTool] {
			t.Errorf("dev tool %q must NOT appear in selection when developer_mode=false, but it did", devTool)
		}
	}

	// Non-dev builtins must still appear.
	if !names["nanite_todo_create"] {
		t.Error("non-dev builtin nanite_todo_create must appear when developer_mode=false")
	}
}

// TestSelectToolsAsProvider_DevToolsIncludedWhenDevModeOn verifies that
// dev tools appear in the selection when developer_mode is true.
func TestSelectToolsAsProvider_DevToolsIncludedWhenDevModeOn(t *testing.T) {
	tb := New(nil, nil, DefaultConfig())
	tb.DeveloperModeFunc = func() bool { return true }

	tb.Builtins.RegisterBuiltins("dev", []provider.ToolDefinition{
		{Name: "dev_bash", Description: "Execute shell commands"},
		{Name: "dev_read", Description: "Read files"},
	})

	res, err := tb.SelectToolsAsProvider(context.Background(), "general", nil, "", "agent-1")
	if err != nil {
		t.Fatalf("SelectToolsAsProvider: %v", err)
	}

	names := toolNames(res.Tools)
	if !names["dev_bash"] {
		t.Error("dev_bash must appear in selection when developer_mode=true")
	}
	if !names["dev_read"] {
		t.Error("dev_read must appear in selection when developer_mode=true")
	}
}

// TestSelectToolsAsProvider_MCPDevToolsExcludedWhenDevModeOff verifies that
// MCP-prefixed dev tools (mcp__dev__dev_bash etc.) are also filtered when
// developer_mode is false. This covers the case where dev tools are discovered
// through the MCP Manager rather than the builtin registry.
func TestSelectToolsAsProvider_MCPDevToolsExcludedWhenDevModeOff(t *testing.T) {
	mgr := mcp.NewManager()
	if err := mgr.AddServer("dev", &mockTransport{tools: []mcp.Tool{
		{Name: "dev_bash", Description: "Execute shell commands"},
		{Name: "dev_read", Description: "Read files"},
	}}, mcp.TierBuiltin); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	if err := mgr.AddServer("general", &mockTransport{tools: []mcp.Tool{
		{Name: "web_fetch", Description: "Fetch a URL"},
	}}, mcp.TierBuiltin); err != nil {
		t.Fatalf("AddServer general: %v", err)
	}
	tb := New(mgr, nil, DefaultConfig())
	tb.DeveloperModeFunc = func() bool { return false }
	mgr.Broker = tb.LocalBroker

	if err := mgr.DiscoverTools(context.Background()); err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}

	res, err := tb.SelectToolsAsProvider(context.Background(), "general", nil, "", "agent-1")
	if err != nil {
		t.Fatalf("SelectToolsAsProvider: %v", err)
	}

	names := toolNames(res.Tools)
	if !names["mcp__general__web_fetch"] {
		t.Error("mcp__general__web_fetch must appear in selection to prove MCP tools were registered")
	}
	if names["mcp__dev__dev_bash"] {
		t.Error("mcp__dev__dev_bash must NOT appear in selection when developer_mode=false")
	}
	if names["mcp__dev__dev_read"] {
		t.Error("mcp__dev__dev_read must NOT appear in selection when developer_mode=false")
	}
}

// ---------------------------------------------------------------------------
// CallTool — dev_mode gate at execution time
// ---------------------------------------------------------------------------

// TestCallTool_DevToolsDeniedWhenDevModeOff verifies that CallTool returns an
// error (rather than executing) for dev_* tools when developer_mode is false.
// This is the execution-time backstop — it applies even if somehow a dev tool
// makes it past the selection-time filter.
func TestCallTool_DevToolsDeniedWhenDevModeOff(t *testing.T) {
	mgr := mcp.NewManager()
	if err := mgr.AddServer("dev", &mockTransport{tools: []mcp.Tool{
		{Name: "dev_bash", Description: "Execute shell commands"},
		{Name: "dev_read", Description: "Read files"},
	}}, mcp.TierBuiltin); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	_ = mgr.DiscoverTools(context.Background())

	tb := New(mgr, nil, DefaultConfig())
	tb.DeveloperModeFunc = func() bool { return false }

	devToolCases := []struct {
		callName string // name the LLM would use
	}{
		{"dev_bash"},
		{"dev_read"},
		{"mcp__dev__dev_bash"},
		{"mcp__dev__dev_read"},
	}

	for _, tc := range devToolCases {
		t.Run(tc.callName, func(t *testing.T) {
			_, err := tb.CallTool(context.Background(), "agent-1", tc.callName, map[string]any{})
			if err == nil {
				t.Fatalf("expected error for dev tool %q when developer_mode=false, got nil", tc.callName)
			}
			if !strings.Contains(err.Error(), "developer_mode") {
				t.Errorf("expected error to mention developer_mode, got: %v", err)
			}
		})
	}
}

// TestCallTool_DevToolsAllowedWhenDevModeOn verifies that CallTool routes dev
// tools normally when developer_mode is true.  We use a mock transport that
// returns a known string so the test is deterministic without a real process.
func TestCallTool_DevToolsAllowedWhenDevModeOn(t *testing.T) {
	mgr := mcp.NewManager()
	if err := mgr.AddServer("dev", &mockDevTransport{}, mcp.TierBuiltin); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	_ = mgr.DiscoverTools(context.Background())

	tb := New(mgr, nil, DefaultConfig())
	tb.DeveloperModeFunc = func() bool { return true }

	result, err := tb.CallTool(context.Background(), "agent-1", "mcp__dev__dev_bash", map[string]any{"command": "echo hi"})
	if err != nil {
		t.Fatalf("expected success for dev tool when developer_mode=true, got: %v", err)
	}
	if !strings.Contains(result, "mock-dev-result") {
		t.Errorf("expected mock-dev-result in output, got: %q", result)
	}
}

// TestCallTool_NonDevToolsUnaffectedByDevMode verifies that non-dev tools are
// unaffected by the developer_mode flag — they still execute when devMode=false.
func TestCallTool_NonDevToolsUnaffectedByDevMode(t *testing.T) {
	mgr := mcp.NewManager()
	if err := mgr.AddServer("general", &mockReturnsOKTransport{}, mcp.TierBuiltin); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	_ = mgr.DiscoverTools(context.Background())

	tb := New(mgr, nil, DefaultConfig())
	tb.DeveloperModeFunc = func() bool { return false }

	result, err := tb.CallTool(context.Background(), "agent-1", "mcp__general__web_fetch", map[string]any{"url": "https://example.com"})
	if err != nil {
		t.Fatalf("non-dev tool must not be blocked by developer_mode gate, got: %v", err)
	}
	if !strings.Contains(result, "ok") {
		t.Errorf("expected 'ok' in output, got: %q", result)
	}
}

// TestCallToolWithPolicyCheck_DevToolsDeniedWhenDevModeOff verifies that the
// policy-check wrapper also enforces the developer_mode gate (it delegates to
// CallTool so the gate is inherited automatically).
func TestCallToolWithPolicyCheck_DevToolsDeniedWhenDevModeOff(t *testing.T) {
	mgr := mcp.NewManager()
	if err := mgr.AddServer("dev", &mockTransport{tools: []mcp.Tool{
		{Name: "dev_read", Description: "Read files"},
	}}, mcp.TierBuiltin); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	_ = mgr.DiscoverTools(context.Background())

	tb := New(mgr, nil, DefaultConfig())
	tb.DeveloperModeFunc = func() bool { return false }

	_, err := tb.CallToolWithPolicyCheck(context.Background(), "agent-1", "dev_read", map[string]any{"path": "/tmp/safe"})
	if err == nil {
		t.Fatal("expected error for dev_read via policy-check wrapper when developer_mode=false")
	}
	if !strings.Contains(err.Error(), "developer_mode") {
		t.Errorf("expected developer_mode in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// DeveloperModeFunc=nil + Store=nil  →  fail-closed (false)
// ---------------------------------------------------------------------------

func TestDevModeFallClosed_NoStoreNoFunc(t *testing.T) {
	tb := New(nil, nil, DefaultConfig())
	tb.Builtins.RegisterBuiltins("dev", []provider.ToolDefinition{
		{Name: "dev_bash", Description: "Shell"},
	})

	// When neither DeveloperModeFunc nor Store is set, dev tools must be absent.
	res, err := tb.SelectToolsAsProvider(context.Background(), "general", nil, "", "agent-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	names := toolNames(res.Tools)
	if names["dev_bash"] {
		t.Error("dev_bash must NOT appear when no store and no DeveloperModeFunc (fail-closed)")
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// toolNames converts a slice of ToolDefinition to a set of names for easy
// membership tests.
func toolNames(defs []provider.ToolDefinition) map[string]bool {
	out := make(map[string]bool, len(defs))
	for _, d := range defs {
		out[d.Name] = true
	}
	return out
}

// mockDevTransport is a mock MCPTransport for the "dev" server that returns a
// canned success result so CallTool tests don't need a real shell.
type mockDevTransport struct{}

func (m *mockDevTransport) ListTools(_ context.Context) ([]mcp.Tool, error) {
	return []mcp.Tool{
		{Name: "dev_bash", Description: "Execute shell commands"},
		{Name: "dev_read", Description: "Read files"},
	}, nil
}

func (m *mockDevTransport) CallTool(_ context.Context, _ string, _ map[string]any) (*mcp.ToolResult, error) {
	return &mcp.ToolResult{
		Content: []mcp.ToolContent{{Type: "text", Text: "mock-dev-result"}},
	}, nil
}

// mockReturnsOKTransport is a mock MCPTransport for non-dev servers.
type mockReturnsOKTransport struct{}

func (m *mockReturnsOKTransport) ListTools(_ context.Context) ([]mcp.Tool, error) {
	return []mcp.Tool{
		{Name: "web_fetch", Description: "Fetch a URL"},
	}, nil
}

func (m *mockReturnsOKTransport) CallTool(_ context.Context, _ string, _ map[string]any) (*mcp.ToolResult, error) {
	return &mcp.ToolResult{
		Content: []mcp.ToolContent{{Type: "text", Text: "ok"}},
	}, nil
}
