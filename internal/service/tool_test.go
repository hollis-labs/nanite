package service

import (
	"context"
	"fmt"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// --- stub MCP manager for tool tests ---

type stubMCPManager struct {
	tools       map[string]string // toolName -> result
	execErr     error
	serverTools map[string][]llmtypes.ToolDefinition
}

func (m *stubMCPManager) ExecuteTool(_ context.Context, name string, _ map[string]any) (string, error) {
	if m.execErr != nil {
		return "", m.execErr
	}
	if result, ok := m.tools[name]; ok {
		return result, nil
	}
	return "ok", nil
}

// --- tests ---

func TestToolService_Execute_NoClients(t *testing.T) {
	svc := NewToolService(nil, nil, nil)
	result, err := svc.Execute(context.Background(), "agent-1", "some_tool", nil)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true when no clients configured")
	}
	if result.Output == "" {
		t.Error("expected non-empty error output")
	}
}

func TestToolService_ListSummaries_NilClient(t *testing.T) {
	svc := NewToolService(nil, nil, nil)
	summaries := svc.ListSummaries()
	if summaries != nil {
		t.Errorf("expected nil summaries, got %d", len(summaries))
	}
}

func TestToolService_HandleRequestTools_NilClient(t *testing.T) {
	svc := NewToolService(nil, nil, nil)
	_, _, err := svc.HandleRequestTools(context.Background(), nil)
	if err == nil {
		t.Error("expected error when tool client is nil")
	}
}

func TestToolService_SelectForAgent_NoTools(t *testing.T) {
	reader := newStubReader()
	reader.addAgent(&store.AgentProfile{ID: "a1", Slug: "test", Status: "active"})

	svc := NewToolService(nil, nil, reader)
	sel, err := svc.SelectForAgent(context.Background(), "s1", "a1", "hello world", "", 0)
	if err != nil {
		t.Fatalf("SelectForAgent: %v", err)
	}
	if len(sel.Tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(sel.Tools))
	}
	if sel.Progressive {
		t.Error("progressive should be false with no tools")
	}
}

// TestSelectForAgent_LateAlphabetAllowlistedToolSurvivesCap is the
// regression test for CW-20260815-0011: the root cause of a live
// Orchestrator durable-agent session being unable to find torque_task_get
// despite it being declared in the profile's own tools allowlist.
//
// MaxSelectedTools used to be applied INSIDE broker selection (SelectTools),
// before the schema-v2 tools allowlist (filterToolsByAllowlist) ever ran —
// a correctly-declared, correctly-permitted tool sitting past index 15 in
// the broker's raw (unranked) registration order was truncated out before
// its own allowlist got a chance to keep it. This test registers more
// tools than the cap, with the wanted tool registered LAST, declares an
// allowlist naming ONLY that tool, and asserts it survives — proving the
// cap now runs after allowlist filtering (FinalizeToolSelection), not
// before it.
func TestSelectForAgent_LateAlphabetAllowlistedToolSurvivesCap(t *testing.T) {
	tc := toolclient.New(mcp.NewManager(), nil, toolclient.DefaultConfig())

	const wanted = "torque_task_get"
	fillerCount := toolclient.MaxSelectedTools + 5
	tools := make([]llmtypes.ToolDefinition, 0, fillerCount+1)
	for i := 0; i < fillerCount; i++ {
		tools = append(tools, llmtypes.ToolDefinition{
			Name:        fmt.Sprintf("torque_filler_%03d", i),
			Description: "filler tool",
		})
	}
	// Registered LAST — past MaxSelectedTools in the catalog's raw
	// (unranked) registration order, exactly the failure mode described in
	// the ticket ("alphabetically late among Torque's ~93-98 native
	// tools").
	tools = append(tools, llmtypes.ToolDefinition{Name: wanted, Description: "Fetch a task"})
	tc.RegisterTools(tools)

	reader := newStubReader()
	reader.addAgent(&store.AgentProfile{
		ID:     "orchestrator-1",
		Slug:   "orchestrator",
		Status: "active",
		Tools:  fmt.Sprintf(`["%s"]`, wanted),
	})

	svc := NewToolService(tc, nil, reader)
	sel, err := svc.SelectForAgent(context.Background(), "session-1", "orchestrator-1", "get the task status", "", 0)
	if err != nil {
		t.Fatalf("SelectForAgent: %v", err)
	}

	found := false
	for _, tdef := range sel.Tools {
		if tdef.Name == wanted {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected allowlisted tool %q to survive selection despite being registered past MaxSelectedTools; got tools: %v",
			wanted, toolNames(sel.Tools))
	}
	if len(sel.Tools) != 1 {
		t.Errorf("expected exactly 1 tool (the allowlist), got %d: %v", len(sel.Tools), toolNames(sel.Tools))
	}
}

// --- helper unit tests ---

func TestExtractIntent(t *testing.T) {
	tests := []struct {
		input       string
		wantIntent  string
		wantMinHint int
	}{
		{"", "general", 0},
		{"create a task for the sprint", "create task sprint", 2},
		{"hi", "general", 0}, // too short, all filtered
	}

	for _, tt := range tests {
		intent, hints := extractIntent(tt.input)
		if intent != tt.wantIntent {
			t.Errorf("extractIntent(%q) intent = %q, want %q", tt.input, intent, tt.wantIntent)
		}
		if len(hints) < tt.wantMinHint {
			t.Errorf("extractIntent(%q) hints len = %d, want >= %d", tt.input, len(hints), tt.wantMinHint)
		}
	}
}

func TestFilterToolsByAllowlist(t *testing.T) {
	// Uniform agent-facing names (ADR-002 — no `mcp__server__` prefix).
	tools := []llmtypes.ToolDefinition{
		{Name: "engine_task_create"},
		{Name: "engine_task_list"},
		{Name: "context_search"},
		{Name: "dev_read"},
	}

	// Empty allowlist — no filtering.
	filtered := filterToolsByAllowlist(tools, "")
	if len(filtered) != 4 {
		t.Errorf("empty allowlist: got %d tools, want 4", len(filtered))
	}

	// Specific allowlist using uniform-name globs.
	filtered = filterToolsByAllowlist(tools, `["engine_*", "dev_read"]`)
	if len(filtered) != 3 {
		t.Errorf("specific allowlist: got %d tools, want 3", len(filtered))
	}

	// No match.
	filtered = filterToolsByAllowlist(tools, `["nonexistent_tool"]`)
	if len(filtered) != 0 {
		t.Errorf("no-match allowlist: got %d tools, want 0", len(filtered))
	}
}

// TestCountMCPOriginTools verifies the post-ADR-002 builtin/MCP-origin
// distinction: instead of pattern-matching on `mcp__` prefix, the
// counter consults the toolclient's BuiltinToolRegistry.
func TestCountMCPOriginTools(t *testing.T) {
	// nil toolClient — pessimistic: count everything as MCP-origin.
	tools := []llmtypes.ToolDefinition{
		{Name: "task_create"},
		{Name: "dev_read"},
	}
	if got := countMCPOriginTools(nil, tools); got != 2 {
		t.Errorf("countMCPOriginTools(nil) = %d, want 2 (pessimistic)", got)
	}

	// With a toolclient that knows two builtins, only the unknown name counts.
	tc := toolclient.New(nil, nil, toolclient.DefaultConfig())
	tc.Builtins.RegisterBuiltins("dev", []llmtypes.ToolDefinition{
		{Name: "dev_read"},
	})
	tc.Builtins.RegisterBuiltins("self", []llmtypes.ToolDefinition{
		{Name: "request_tools"},
	})
	mixed := []llmtypes.ToolDefinition{
		{Name: "task_create"},    // MCP-origin
		{Name: "dev_read"},       // builtin
		{Name: "context_search"}, // MCP-origin
		{Name: "request_tools"},  // builtin (meta)
	}
	if got := countMCPOriginTools(tc, mixed); got != 2 {
		t.Errorf("countMCPOriginTools = %d, want 2 (only task_create + context_search)", got)
	}
}

func TestBuildToolCatalog(t *testing.T) {
	// Empty.
	if got := chat.BuildToolCatalog(nil); got != "" {
		t.Errorf("empty catalog = %q, want empty", got)
	}
}
