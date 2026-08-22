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
// before the roster-membership filter ever ran — a correctly-declared,
// correctly-permitted tool sitting past index 15 in the broker's raw
// (unranked) registration order was truncated out before its own roster
// filter got a chance to keep it. This test registers more tools than the
// cap, with the wanted tool registered LAST, grants ONLY that tool via
// agent_tools (Phase 4 item 05's read path — the FK-based replacement for
// the old schema-v2 tools allowlist this test used to exercise), and
// asserts it survives — proving the cap now runs after agent_tools
// filtering (FinalizeToolSelection), not before it.
func TestSelectForAgent_LateAlphabetAllowlistedToolSurvivesCap(t *testing.T) {
	st := newKnownToolsTestStore(t)
	ctx := context.Background()

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

	tc := toolclient.New(mcp.NewManager(), st, toolclient.DefaultConfig())
	tc.RegisterTools(tools)

	// Mirrors container.go's real sync call, which appends request_tools
	// to the catalog it syncs (see that file's comment) — without it,
	// SyncKnownTools' mark-unavailable pass would demote the migration-
	// seeded request_tools row simply for not being in this test's small
	// registered-tools catalog.
	SyncKnownTools(ctx, st, append(append([]llmtypes.ToolDefinition{}, tools...), toolclient.RequestToolsMetaTool()), func(string) bool { return false })
	wantedTool, err := st.GetKnownToolByName(ctx, wanted)
	if err != nil {
		t.Fatalf("GetKnownToolByName(%s): %v", wanted, err)
	}

	agent := &store.AgentProfile{Name: "Orchestrator", Slug: "orchestrator", SystemPrompt: "Test."}
	if err := st.CreateAgent(context.Background(), agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if err := st.GrantAgentTool(ctx, agent.ID, wantedTool.ID, "explicit"); err != nil {
		t.Fatalf("GrantAgentTool: %v", err)
	}

	svc := NewToolService(tc, nil, st)
	sel, err := svc.SelectForAgent(context.Background(), "session-1", agent.ID, "get the task status", "", 0)
	if err != nil {
		t.Fatalf("SelectForAgent: %v", err)
	}

	names := toolNames(sel.Tools)
	found := false
	for _, n := range names {
		if n == wanted {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected granted tool %q to survive selection despite being registered past MaxSelectedTools; got tools: %v",
			wanted, names)
	}
	// Only the granted tool plus the always_included escape hatch
	// (request_tools — tool_list/tool_describe aren't registered on this
	// toolclient, so they don't resolve) should survive; no filler leaks
	// through.
	for _, n := range names {
		if n != wanted && n != "request_tools" {
			t.Errorf("unexpected tool %q survived selection (want only %q + escape hatch): %v", n, wanted, names)
		}
	}
}

// TestSelectForAgent_AlwaysIncludedSurvivesZeroGrants is the acceptance
// test for this task's item 5 / Done-means bullet 3: an agent with a real
// agent_profiles row and ZERO explicit agent_tools grants (no legacy
// backfill run, no operator grant) must still see the
// known_tools.always_included=true escape hatch (request_tools/tool_list/
// tool_describe) — and must NOT see an ordinary, non-always-included
// catalog tool it was never granted (item 4: no live "unrestricted"
// bypass for a zero-grant agent).
func TestSelectForAgent_AlwaysIncludedSurvivesZeroGrants(t *testing.T) {
	st := newKnownToolsTestStore(t)
	ctx := context.Background()

	catalog := []llmtypes.ToolDefinition{{Name: "dev_read", Description: "Reads a file."}}
	tc := toolclient.New(mcp.NewManager(), st, toolclient.DefaultConfig())
	tc.RegisterTools(catalog)
	// Mirrors container.go's real sync call (which appends request_tools
	// to the synced catalog — see that file's comment) so this test
	// exercises the realistic "request_tools row stays available" case.
	SyncKnownTools(ctx, st, append(append([]llmtypes.ToolDefinition{}, catalog...), toolclient.RequestToolsMetaTool()), func(string) bool { return true })

	agent := &store.AgentProfile{Name: "Fresh", Slug: "fresh-agent", SystemPrompt: "Test."}
	if err := st.CreateAgent(context.Background(), agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	// Deliberately NO agent_tools grants and NO legacy backfill run.

	svc := NewToolService(tc, nil, st)
	sel, err := svc.SelectForAgent(context.Background(), "session-1", agent.ID, "hello", "", 0)
	if err != nil {
		t.Fatalf("SelectForAgent: %v", err)
	}

	names := toolNames(sel.Tools)
	escapeFound := false
	for _, n := range names {
		if n == "request_tools" {
			escapeFound = true
		}
		if n == "dev_read" {
			t.Errorf("dev_read should NOT survive selection with zero agent_tools grants (no live unrestricted bypass); got %v", names)
		}
	}
	if !escapeFound {
		t.Errorf("expected always_included escape-hatch tool %q to survive zero-grant selection; got %v", "request_tools", names)
	}
}

// TestFilterToolsByAgentTools is the direct unit-level coverage for the
// new roster-membership filter, superseding TestFilterToolsByAllowlist
// (deleted — filterToolsByAllowlist itself was deleted from the live path
// by this task; see Work Log).
func TestFilterToolsByAgentTools(t *testing.T) {
	st := newKnownToolsTestStore(t)
	ctx := context.Background()

	catalog := []llmtypes.ToolDefinition{
		{Name: "engine_task_create"},
		{Name: "engine_task_list"},
		{Name: "context_search"},
	}
	SyncKnownTools(ctx, st, catalog, func(string) bool { return true })

	agent := &store.AgentProfile{Name: "Grants", Slug: "grants-agent", SystemPrompt: "Test."}
	if err := st.CreateAgent(context.Background(), agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	createTool, err := st.GetKnownToolByName(ctx, "engine_task_create")
	if err != nil {
		t.Fatalf("GetKnownToolByName: %v", err)
	}
	if err := st.GrantAgentTool(ctx, agent.ID, createTool.ID, "explicit"); err != nil {
		t.Fatalf("GrantAgentTool: %v", err)
	}

	filtered := filterToolsByAgentTools(ctx, st, agent.ID, catalog)
	if len(filtered) != 1 || filtered[0].Name != "engine_task_create" {
		t.Fatalf("filterToolsByAgentTools = %v, want exactly [engine_task_create]", toolNames(filtered))
	}

	// nil store is a documented no-op pass-through.
	if got := filterToolsByAgentTools(ctx, nil, agent.ID, catalog); len(got) != len(catalog) {
		t.Errorf("nil store: expected pass-through, got %d tools", len(got))
	}

	// Zero grants -> zero tools (no live "unrestricted" bypass, item 4).
	other := &store.AgentProfile{Name: "NoGrants", Slug: "no-grants-agent", SystemPrompt: "Test."}
	if err := st.CreateAgent(context.Background(), other); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if got := filterToolsByAgentTools(ctx, st, other.ID, catalog); len(got) != 0 {
		t.Errorf("zero grants: expected 0 tools, got %d: %v", len(got), toolNames(got))
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
