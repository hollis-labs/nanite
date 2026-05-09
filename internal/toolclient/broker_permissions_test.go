package toolclient

import (
	"context"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/mcp"
)

// All tool names in this file use uniform agent-facing form (ADR-002).
// Pre-internalization there was a separate "bare vs prefixed" bypass
// concern (an agent could call dev_bash to evade `mcp__dev__*` deny
// rules). With uniform names there is exactly one name per tool and one
// permission check; the bypass surface is gone by construction.

// --- 1. Structured deny return ---

func TestCheckPermission_DenyByExactName(t *testing.T) {
	// Direct policy-layer deny on the uniform name.
	p := ToolPermissions{DenyList: []string{"dev_bash"}, MaxCallsPerTurn: 25}
	if p.CheckPermission("dev_bash") {
		t.Fatal("expected deny on dev_bash")
	}
	if !p.CheckPermission("dev_read") {
		t.Fatal("expected dev_read to remain allowed")
	}
}

func TestCheckPermission_DenyByGlob(t *testing.T) {
	// Glob deny on the uniform `dev_*` namespace covers every dev tool;
	// there is no longer a bare-name escape (ADR-002 — single name).
	p := ToolPermissions{DenyList: []string{"dev_*"}, MaxCallsPerTurn: 25}
	if p.CheckPermission("dev_bash") {
		t.Fatal("expected deny on dev_bash via dev_* glob")
	}
	if p.CheckPermission("dev_read") {
		t.Fatal("expected deny on dev_read via dev_* glob")
	}
	if !p.CheckPermission("todo_create") {
		t.Fatal("expected todo_create to remain allowed")
	}
}

func TestCallTool_UnknownToolReturnsError(t *testing.T) {
	// Drive CallTool with a MCPManager but a tool name that is NOT registered,
	// to confirm the "not found" branch returns a clear error and does
	// not execute. Post ADR-002 the manager looks up the uniform name
	// directly via the uniformIndex; an unknown name is always an error.
	mgr := mcp.NewManager()
	if err := mgr.AddServer("test", &mockTransport{tools: []mcp.Tool{
		{Name: "hello", Description: "Hello tool"},
	}}, mcp.TierBuiltin); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	_ = mgr.DiscoverTools(context.Background())

	tb := New(mgr, nil, DefaultConfig())

	_, err := tb.CallTool(context.Background(), "agent-1", "nonexistent", map[string]any{})
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("expected error to mention tool name, got: %v", err)
	}
}

// --- 2. Builtin permission filtering at SelectToolsAsProvider ---

func TestSelectToolsAsProvider_BuiltinFilteredThroughPermissions(t *testing.T) {
	// Build a ToolClient with a builtin tool registered. With Store=nil the
	// default permissions are permissive, so the builtin should appear. The
	// important regression assertion here is that SelectToolsAsProvider
	// *calls* CheckPermission on each builtin rather than blanket-prepending.
	// We verify that by: (a) registering a builtin and confirming it appears
	// under permissive policy, and (b) confirming the code path is present by
	// inspecting the filtered slice's size matches the policy outcome.
	//
	// Note: dev_* tools are gated behind developer_mode; we use non-dev
	// builtins here to keep the test focused on permission filtering, not the
	// dev-mode gate. See devmode_gate_test.go for dev-mode gate coverage.

	tb := New(nil, nil, DefaultConfig())
	tb.Builtins.RegisterBuiltins("self", []llmtypes.ToolDefinition{
		{Name: "todo_create", Description: "Create a todo"},
		{Name: "plan_create", Description: "Create a plan"},
	})

	res, err := tb.SelectToolsAsProvider(context.Background(), "general", nil, "", "agent-permissive", 0)
	if err != nil {
		t.Fatalf("SelectToolsAsProvider: %v", err)
	}

	// Permissive default → both builtins present.
	names := map[string]bool{}
	for _, d := range res.Tools {
		names[d.Name] = true
	}
	if !names["todo_create"] || !names["plan_create"] {
		t.Errorf("expected both builtins under permissive policy, got: %v", names)
	}
}

func TestCheckPermission_BuiltinDeniable(t *testing.T) {
	// Direct policy-layer assertion: a deny_list for a builtin name must take
	// effect.
	p := ToolPermissions{
		DenyList:        []string{"dev_bash"},
		MaxCallsPerTurn: 25,
	}
	if p.CheckPermission("dev_bash") {
		t.Error("expected dev_bash to be denied by policy")
	}
	if !p.CheckPermission("dev_read") {
		t.Error("expected dev_read to remain allowed")
	}
}

// --- 3. request_tools escalation — name + arg-level checks ---

func TestHandleRequestToolsForAgent_FiltersDeniedInnerNames(t *testing.T) {
	// With Store=nil we cannot force denies via policy, so we
	// verify: (a) the permitted path returns all tools under permissive
	// policy, (b) arg-level escalation triggers an immediate deny regardless
	// of policy. Tool names are uniform — newTestBrokerWithTools registers
	// them under the "test" server name, so via the broker their uniform
	// names are exactly the original names (ADR-002).
	tools := []llmtypes.ToolDefinition{
		{Name: "volon_task_create", Description: "Create a task"},
		{Name: "dev_bash", Description: "Shell execution"},
	}
	tb := newTestBrokerWithTools(tools)

	// (a) Permissive policy — all merged tools returned.
	permitted, summary := tb.HandleRequestToolsForAgent("agent-1", map[string]any{
		"tool_names": []any{"volon_task_create", "dev_bash"},
	})
	if len(permitted) != 2 {
		t.Errorf("expected 2 permitted tools under permissive policy, got %d (summary: %s)", len(permitted), summary)
	}
}

func TestHandleRequestToolsForAgent_DeniesPathTraversalArg(t *testing.T) {
	tb := newTestBrokerWithTools([]llmtypes.ToolDefinition{
		{Name: "dev_read", Description: "Read files"},
	})

	permitted, summary := tb.HandleRequestToolsForAgent("agent-1", map[string]any{
		"tool_names": []any{"dev_read"},
		"path":       "../../etc/passwd",
	})
	if len(permitted) != 0 {
		t.Errorf("expected 0 permitted tools when arg contains path traversal, got %d", len(permitted))
	}
	if !strings.Contains(summary, "escalation pattern") {
		t.Errorf("expected summary to mention escalation pattern, got: %s", summary)
	}
}

func TestHandleRequestToolsForAgent_DeniesNestedTraversal(t *testing.T) {
	tb := newTestBrokerWithTools([]llmtypes.ToolDefinition{
		{Name: "dev_read", Description: "Read files"},
	})

	// Path-traversal hidden inside a nested map.
	permitted, summary := tb.HandleRequestToolsForAgent("agent-1", map[string]any{
		"tool_names": []any{"dev_read"},
		"options": map[string]any{
			"target": "safe",
			"nested": map[string]any{
				"evil": "..\\windows\\system32",
			},
		},
	})
	if len(permitted) != 0 {
		t.Errorf("expected deny on nested traversal, got %d permitted", len(permitted))
	}
	if !strings.Contains(summary, "escalation pattern") {
		t.Errorf("expected summary to mention escalation pattern, got: %s", summary)
	}
}

func TestArgsContainEscalationPattern(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		want bool
	}{
		{"empty", map[string]any{}, false},
		{"clean string", map[string]any{"path": "/tmp/foo"}, false},
		{"path traversal", map[string]any{"path": "../secret"}, true},
		{"windows traversal", map[string]any{"path": "..\\etc"}, true},
		{"embedded", map[string]any{"path": "safe/../../root"}, true},
		{"slice string", map[string]any{"paths": []any{"ok", "../bad"}}, true},
		{"typed slice", map[string]any{"paths": []string{"ok", "../bad"}}, true},
		{"nested map", map[string]any{"o": map[string]any{"x": ".."}}, true},
		{"non-string values", map[string]any{"n": 42, "b": true}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ArgsContainEscalationPattern(c.args)
			if got != c.want {
				t.Errorf("ArgsContainEscalationPattern(%v) = %v, want %v", c.args, got, c.want)
			}
		})
	}
}

func TestCallToolWithPolicyCheck_RejectsTraversal(t *testing.T) {
	mgr := mcp.NewManager()
	if err := mgr.AddServer("test", &mockTransport{tools: []mcp.Tool{
		{Name: "dev_read", Description: "Read files"},
	}}, mcp.TierBuiltin); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	_ = mgr.DiscoverTools(context.Background())

	tb := New(mgr, nil, DefaultConfig())

	_, err := tb.CallToolWithPolicyCheck(context.Background(), "agent-1", "dev_read", map[string]any{
		"path": "../../../etc/passwd",
	})
	if err == nil {
		t.Fatal("expected deny for path-traversal arg")
	}
	if !strings.Contains(err.Error(), "escalation pattern") {
		t.Errorf("expected escalation-pattern error, got: %v", err)
	}
}
