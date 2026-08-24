package toolclient

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

// TASKS/adhoc/02-remove-tool-permissions-collapse-to-agent-tools.md
// replaced the legacy tool_permissions/CheckPermission execution-time
// backstop in CallTool (and the request_tools inner-tool filter in
// HandleRequestToolsForAgent) with an agent_tools-based check
// (isToolGrantedToAgent). These tests are the direct replacement for the
// service-package TestCallTool_DefenseInDepthDeniesBypassedTool this task
// deleted (internal/service/tool_permissions_render_test.go, which tested
// the retired mechanism) — same load-bearing assertion, new mechanism: a
// tool not granted via agent_tools is denied at CallTool regardless of
// what selection-time filtering did or didn't do, and a fresh agent with
// zero grants sees zero tools allowed, not "everything allowed".

func newStoreForPermTest(t *testing.T) *store.Store {
	t.Helper()
	s, err := storetest.New(t, context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}

func TestCallTool_AgentToolsDeniesUngrantedTool(t *testing.T) {
	s := newStoreForPermTest(t)
	ctx := context.Background()

	agent := &store.AgentProfile{Name: "Readonly", Slug: "readonly-agent-tools", SystemPrompt: "test"}
	if err := s.CreateAgent(context.Background(), agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	readToolID, err := s.UpsertKnownTool(ctx, "read_file", "builtin", "available", "")
	if err != nil {
		t.Fatalf("UpsertKnownTool(read_file): %v", err)
	}
	if _, err := s.UpsertKnownTool(ctx, "write_file", "builtin", "available", ""); err != nil {
		t.Fatalf("UpsertKnownTool(write_file): %v", err)
	}
	if err := s.GrantAgentTool(ctx, agent.ID, readToolID, "explicit"); err != nil {
		t.Fatalf("GrantAgentTool: %v", err)
	}

	mgr := mcp.NewManager()
	if err := mgr.AddServer("test", &mockTransport{tools: []mcp.Tool{
		{Name: "read_file", Description: "Read a file"},
		{Name: "write_file", Description: "Write a file"},
	}}, mcp.TierBuiltin); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	_ = mgr.DiscoverTools(ctx)

	tb := New(mgr, s, DefaultConfig())

	// Sanity on the policy itself.
	if !tb.isToolGrantedToAgent(ctx, agent.ID, "read_file") {
		t.Fatal("read_file should be granted")
	}
	if tb.isToolGrantedToAgent(ctx, agent.ID, "write_file") {
		t.Fatal("write_file should be denied (never granted)")
	}

	// The load-bearing backstop: CallTool denies write_file even though
	// selection-time filtering is bypassed entirely here.
	_, err = tb.CallTool(ctx, agent.ID, "write_file", map[string]any{"path": "/tmp/x"})
	if err == nil {
		t.Fatal("expected permission-denied error from CallTool backstop, got nil")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("expected 'permission denied' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "write_file") {
		t.Errorf("expected error to name the tool 'write_file', got: %v", err)
	}

	// The granted tool still executes normally.
	if _, err := tb.CallTool(ctx, agent.ID, "read_file", map[string]any{}); err != nil {
		t.Errorf("expected read_file (granted) to execute, got err: %v", err)
	}
}

func TestCallTool_FreshAgentZeroToolsAllowedBeforeAnyGrant(t *testing.T) {
	// Done-means acceptance bar (task step 7): a fresh test agent with no
	// agent_tools grants at all must see zero tools allowed -- NOT
	// "everything allowed", which was the old tool_permissions deny-only
	// default (empty allow_list == permissive).
	s := newStoreForPermTest(t)
	ctx := context.Background()

	agent := &store.AgentProfile{Name: "Fresh", Slug: "fresh-agent-tools", SystemPrompt: "test"}
	if err := s.CreateAgent(context.Background(), agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if _, err := s.UpsertKnownTool(ctx, "read_file", "builtin", "available", ""); err != nil {
		t.Fatalf("UpsertKnownTool: %v", err)
	}

	tb := New(nil, s, DefaultConfig())
	if tb.isToolGrantedToAgent(ctx, agent.ID, "read_file") {
		t.Fatal("expected zero tools allowed for a fresh agent with no agent_tools grants")
	}
}

func TestHandleRequestToolsForAgent_AgentToolsDeniesUngrantedInnerName(t *testing.T) {
	s := newStoreForPermTest(t)
	ctx := context.Background()

	agent := &store.AgentProfile{Name: "Restricted", Slug: "restricted-request-tools", SystemPrompt: "test"}
	if err := s.CreateAgent(context.Background(), agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	grantedID, err := s.UpsertKnownTool(ctx, "example_task_create", "builtin", "available", "")
	if err != nil {
		t.Fatalf("UpsertKnownTool(example_task_create): %v", err)
	}
	if _, err := s.UpsertKnownTool(ctx, "dev_bash", "builtin", "available", ""); err != nil {
		t.Fatalf("UpsertKnownTool(dev_bash): %v", err)
	}
	if err := s.GrantAgentTool(ctx, agent.ID, grantedID, "explicit"); err != nil {
		t.Fatalf("GrantAgentTool: %v", err)
	}

	tools := []struct {
		Name        string
		Description string
	}{
		{"example_task_create", "Create a task"},
		{"dev_bash", "Shell execution"},
	}
	mgr := mcp.NewManager()
	mcpTools := make([]mcp.Tool, len(tools))
	for i, t := range tools {
		mcpTools[i] = mcp.Tool{Name: t.Name, Description: t.Description}
	}
	if err := mgr.AddServer("test", &mockTransport{tools: mcpTools}, mcp.TierBuiltin); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	_ = mgr.DiscoverTools(ctx)

	tb := New(mgr, s, DefaultConfig())

	permitted, summary := tb.HandleRequestToolsForAgent(ctx, agent.ID, map[string]any{
		"tool_names": []any{"example_task_create", "dev_bash"},
	})
	if len(permitted) != 1 || permitted[0].Name != "example_task_create" {
		t.Errorf("expected only example_task_create permitted, got %+v (summary: %s)", permitted, summary)
	}
	if !strings.Contains(summary, "dev_bash") {
		t.Errorf("expected summary to mention denied dev_bash, got: %s", summary)
	}
}
