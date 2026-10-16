package toolclient

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/mcp"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

// Dev tools are ordinary tools: nothing in the broker hides or refuses them by
// name. Whether an agent may call one is decided by its agent_tools grants and
// the permission engine, not by a developer flag.

func TestSelectToolsAsProvider_DevBuiltinsAreSelected(t *testing.T) {
	tb := New(nil, nil, DefaultConfig())
	tb.Builtins.RegisterBuiltins("dev", []llmtypes.ToolDefinition{
		{Name: "dev_bash", Description: "Execute shell commands"},
		{Name: "dev_read", Description: "Read files"},
	})
	tb.Builtins.RegisterBuiltins("self", []llmtypes.ToolDefinition{
		{Name: "todo_create", Description: "Create a todo"},
	})

	res, err := tb.SelectToolsAsProvider(context.Background(), "general", nil, "", "agent-1")
	if err != nil {
		t.Fatalf("SelectToolsAsProvider: %v", err)
	}
	names := toolNames(res.Tools)
	for _, want := range []string{"dev_bash", "dev_read", "todo_create"} {
		if !names[want] {
			t.Errorf("%s must appear in selection; tool exposure is not gated by name", want)
		}
	}
}

func TestSelectToolsAsProvider_MCPDevToolsAreSelected(t *testing.T) {
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
	if err := mgr.DiscoverTools(context.Background()); err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}

	res, err := tb.SelectToolsAsProvider(context.Background(), "general", nil, "", "agent-1")
	if err != nil {
		t.Fatalf("SelectToolsAsProvider: %v", err)
	}
	names := toolNames(res.Tools)
	for _, want := range []string{"web_fetch", "dev_bash", "dev_read"} {
		if !names[want] {
			t.Errorf("%s must appear in selection", want)
		}
	}
}

func TestCallTool_DevToolsRouteLikeAnyOtherTool(t *testing.T) {
	mgr := mcp.NewManager()
	if err := mgr.AddServer("dev", &mockDevTransport{}, mcp.TierBuiltin); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	_ = mgr.DiscoverTools(context.Background())

	tb := New(mgr, nil, DefaultConfig())
	result, err := tb.CallTool(context.Background(), "agent-1", "dev_bash", map[string]any{"command": "echo hi"})
	if err != nil {
		t.Fatalf("dev tool must execute when granted, got: %v", err)
	}
	if !strings.Contains(result, "mock-dev-result") {
		t.Errorf("expected mock-dev-result in output, got: %q", result)
	}
}

func TestCallToolWithPolicyCheck_DevToolsAreNotRefusedByName(t *testing.T) {
	mgr := mcp.NewManager()
	if err := mgr.AddServer("dev", &mockDevTransport{}, mcp.TierBuiltin); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	_ = mgr.DiscoverTools(context.Background())

	tb := New(mgr, nil, DefaultConfig())
	if _, err := tb.CallToolWithPolicyCheck(context.Background(), "agent-1", "dev_read", map[string]any{"path": "/tmp/safe"}); err != nil {
		t.Fatalf("policy-check wrapper must not refuse dev_read by name: %v", err)
	}
}

func TestCallTool_NonDevToolsExecute(t *testing.T) {
	mgr := mcp.NewManager()
	if err := mgr.AddServer("general", &mockReturnsOKTransport{}, mcp.TierBuiltin); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	_ = mgr.DiscoverTools(context.Background())

	tb := New(mgr, nil, DefaultConfig())
	result, err := tb.CallTool(context.Background(), "agent-1", "web_fetch", map[string]any{"url": "https://example.com"})
	if err != nil {
		t.Fatalf("non-dev tool must execute, got: %v", err)
	}
	if !strings.Contains(result, "ok") {
		t.Errorf("expected 'ok' in output, got: %q", result)
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// toolNames converts a slice of ToolDefinition to a set of names for easy
// membership tests.
func toolNames(defs []llmtypes.ToolDefinition) map[string]bool {
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
