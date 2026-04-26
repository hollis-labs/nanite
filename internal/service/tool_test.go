package service

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/store"
)

// --- stub MCP manager for tool tests ---

type stubMCPManager struct {
	tools      map[string]string // toolName -> result
	execErr    error
	serverTools map[string][]provider.ToolDefinition
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
	tools := []provider.ToolDefinition{
		{Name: "mcp__engine__task_create"},
		{Name: "mcp__engine__task_list"},
		{Name: "mcp__conduit__search"},
		{Name: "dev_read"},
	}

	// Empty allowlist — no filtering.
	filtered := filterToolsByAllowlist(tools, "")
	if len(filtered) != 4 {
		t.Errorf("empty allowlist: got %d tools, want 4", len(filtered))
	}

	// Specific allowlist.
	filtered = filterToolsByAllowlist(tools, `["mcp__engine__*", "dev_read"]`)
	if len(filtered) != 3 {
		t.Errorf("specific allowlist: got %d tools, want 3", len(filtered))
	}

	// No match.
	filtered = filterToolsByAllowlist(tools, `["nonexistent_tool"]`)
	if len(filtered) != 0 {
		t.Errorf("no-match allowlist: got %d tools, want 0", len(filtered))
	}
}

func TestCountMCPTools(t *testing.T) {
	tools := []provider.ToolDefinition{
		{Name: "mcp__engine__task_create"},
		{Name: "dev_read"},
		{Name: "mcp__conduit__search"},
		{Name: "request_tools"},
	}
	if got := countMCPTools(tools); got != 2 {
		t.Errorf("countMCPTools = %d, want 2", got)
	}
}

func TestBuildToolCatalog(t *testing.T) {
	// Empty.
	if got := chat.BuildToolCatalog(nil); got != "" {
		t.Errorf("empty catalog = %q, want empty", got)
	}
}
