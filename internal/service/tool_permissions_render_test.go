package service

import (
	"context"
	"sort"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-toolbroker/broker"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// CW-20260512-0117 / SP-20260512-0010 — `tool_permissions` JSON honored at
// description-render time. These tests cover the acceptance criteria from
// the ticket:
//
//  1. An agent with `tool_permissions = ["read_file", "list_files"]`
//     (expressed as the canonical allow_list shape) sees ONLY those two
//     tools in its rendered tool list.
//  2. Agent attempting a non-listed tool via the existing CallTool gate is
//     still denied — defense-in-depth backstop, NOT removed by the
//     description filter.
//  3. Two profiles, same dispatch, different permissions → different tool
//     counts in rendered description.
//
// Sibling unit coverage for the broker layer lives in
// internal/toolclient/broker_permissions_test.go. These tests exercise the
// service-layer assembly (SelectForAgent) so any future code path that
// appends tools without filtering — e.g., the discoverAgentMCPTools
// fallback that motivated this fix — regresses loudly.

// buildToolClientForPerms wires a toolclient with a representative tool set
// covering the ticket's acceptance scenarios. Tools are registered as
// broker tools so they flow through SelectToolsAsProvider just as MCP-
// discovered tools would. developer_mode is pinned ON so the orthogonal
// dev-mode gate cannot perturb tool counts here.
func buildToolClientForPerms(t *testing.T) *toolclient.ToolClient {
	t.Helper()
	tc := toolclient.New(nil, nil, toolclient.DefaultConfig())
	tc.DeveloperModeFunc = func() bool { return true }
	tc.RegisterTools([]broker.ToolDefinition{
		{Name: "read_file", Server: "fs", Description: "Read a file"},
		{Name: "list_files", Server: "fs", Description: "List files in a directory"},
		{Name: "write_file", Server: "fs", Description: "Write a file"},
		{Name: "delete_file", Server: "fs", Description: "Delete a file"},
		{Name: "engine_task_create", Server: "engine", Description: "Create a task"},
		{Name: "engine_task_list", Server: "engine", Description: "List tasks"},
	})
	return tc
}

// permissionResolverFor returns a toolclient.PermissionResolver that maps
// the supplied agentID → ToolPermissions. Every other agentID returns
// (zero, false) which signals the toolclient to fall through to the
// store-backed path — with Store=nil in these tests, that returns the
// permissive default.
func permissionResolverFor(agentID string, perms toolclient.ToolPermissions) toolclient.PermissionResolver {
	return func(id string) (toolclient.ToolPermissions, bool) {
		if id == agentID {
			return perms, true
		}
		return toolclient.ToolPermissions{}, false
	}
}

func sortedToolNames(tools []llmtypes.ToolDefinition) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	sort.Strings(out)
	return out
}

// TestSelectForAgent_ToolPermissionsAllowListHonored is the primary
// acceptance assertion: an agent with allow_list = ["read_file",
// "list_files"] sees EXACTLY those two tools.
func TestSelectForAgent_ToolPermissionsAllowListHonored(t *testing.T) {
	const agentID = "agent-readonly"

	tc := buildToolClientForPerms(t)
	tc.PermissionResolver = permissionResolverFor(agentID, toolclient.ToolPermissions{
		AllowList:       []string{"read_file", "list_files"},
		MaxCallsPerTurn: 25,
	})

	reader := newStubReader()
	reader.addAgent(&store.AgentProfile{
		ID:     agentID,
		Slug:   "readonly",
		Status: "active",
		// Tools left empty so we exercise the tool_permissions path in
		// isolation from the schema-v2 Tools allowlist (which the older
		// filterToolsByAllowlist helper would otherwise apply on top).
	})

	svc := NewToolService(tc, nil, reader).(*toolServiceImpl)
	sel, err := svc.SelectForAgent(context.Background(), "s1", agentID, "read some files", "", 0)
	if err != nil {
		t.Fatalf("SelectForAgent: %v", err)
	}

	got := sortedToolNames(sel.Tools)
	want := []string{"list_files", "read_file"}
	if !sliceEq(got, want) {
		t.Errorf("rendered tool list = %v, want exactly %v", got, want)
	}

	// Negative: forbidden tools must not appear.
	for _, forbidden := range []string{"write_file", "delete_file", "engine_task_create", "engine_task_list"} {
		for _, n := range got {
			if n == forbidden {
				t.Errorf("rendered tool list unexpectedly contains denied tool %q: %v", forbidden, got)
			}
		}
	}
}

// TestSelectForAgent_ToolPermissionsTwoProfilesDifferentCounts is the
// "two profiles, same dispatch, different permissions → different tool
// counts" acceptance test from the ticket.
func TestSelectForAgent_ToolPermissionsTwoProfilesDifferentCounts(t *testing.T) {
	const userMessage = "list files and create a task"

	const readerID = "agent-reader"
	const editorID = "agent-editor"

	readerPerms := toolclient.ToolPermissions{
		AllowList:       []string{"read_file", "list_files"},
		MaxCallsPerTurn: 25,
	}
	editorPerms := toolclient.ToolPermissions{
		AllowList:       []string{"read_file", "list_files", "write_file", "engine_task_create"},
		MaxCallsPerTurn: 25,
	}

	// Each toolclient gets its OWN resolver so we can verify the per-
	// profile materialization is genuinely different (not just coincident
	// state in a shared client). The resolver lookup is by-ID, so a
	// single toolclient with a two-key map would also work — keeping
	// them isolated mirrors how per-session toolclients would be wired.
	tcReader := buildToolClientForPerms(t)
	tcReader.PermissionResolver = permissionResolverFor(readerID, readerPerms)
	tcEditor := buildToolClientForPerms(t)
	tcEditor.PermissionResolver = permissionResolverFor(editorID, editorPerms)

	mkSvc := func(tc *toolclient.ToolClient, agentID, slug string) *toolServiceImpl {
		reader := newStubReader()
		reader.addAgent(&store.AgentProfile{
			ID:     agentID,
			Slug:   slug,
			Status: "active",
		})
		return NewToolService(tc, nil, reader).(*toolServiceImpl)
	}

	readerSel, err := mkSvc(tcReader, readerID, "reader").
		SelectForAgent(context.Background(), "s1", readerID, userMessage, "", 0)
	if err != nil {
		t.Fatalf("SelectForAgent(reader): %v", err)
	}
	editorSel, err := mkSvc(tcEditor, editorID, "editor").
		SelectForAgent(context.Background(), "s1", editorID, userMessage, "", 0)
	if err != nil {
		t.Fatalf("SelectForAgent(editor): %v", err)
	}

	readerNames := sortedToolNames(readerSel.Tools)
	editorNames := sortedToolNames(editorSel.Tools)

	if len(readerNames) >= len(editorNames) {
		t.Errorf("expected reader tool count < editor tool count; got reader=%d (%v), editor=%d (%v)",
			len(readerNames), readerNames, len(editorNames), editorNames)
	}
	// Concrete shape: reader sees only the two read-side tools; editor
	// additionally sees write_file and engine_task_create.
	wantReader := []string{"list_files", "read_file"}
	if !sliceEq(readerNames, wantReader) {
		t.Errorf("reader profile = %v, want %v", readerNames, wantReader)
	}
	for _, must := range []string{"write_file", "engine_task_create"} {
		found := false
		for _, n := range editorNames {
			if n == must {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("editor profile missing %q (got %v)", must, editorNames)
		}
	}
}

// TestCallTool_DefenseInDepthDeniesBypassedTool proves the gate at
// ToolClient.CallTool still denies a tool the description filter would
// have hidden. The agent's allow_list is ["read_file"]; we drive
// CallTool with "write_file" (bypassing the SelectForAgent pipeline) and
// expect a permission-denied error. This is the load-bearing backstop
// the description filter does NOT replace — defense in depth, per the
// harness-restoration design session and the broker.go CallTool doc
// comment.
func TestCallTool_DefenseInDepthDeniesBypassedTool(t *testing.T) {
	const agentID = "agent-readonly"

	// An empty MCP manager is fine: CallTool's CheckPermission call fires
	// before MCPManager lookup, so the manager is never consulted on the
	// deny path. If permission denial regresses, the test would see the
	// "no MCP manager configured" error instead — which is still an
	// error, but the wrong shape; the test asserts on the specific
	// "permission denied" string to catch that.
	tc := toolclient.New(mcp.NewManager(), nil, toolclient.DefaultConfig())
	tc.PermissionResolver = permissionResolverFor(agentID, toolclient.ToolPermissions{
		AllowList:       []string{"read_file"},
		MaxCallsPerTurn: 25,
	})

	// Sanity on the policy itself.
	if !tc.CheckPermission(agentID, "read_file") {
		t.Fatal("read_file should be permitted")
	}
	if tc.CheckPermission(agentID, "write_file") {
		t.Fatal("write_file should be denied by allow_list")
	}

	_, err := tc.CallTool(context.Background(), agentID, "write_file", map[string]any{"path": "/tmp/x"})
	if err == nil {
		t.Fatal("expected permission-denied error from CallTool backstop, got nil")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("expected 'permission denied' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "write_file") {
		t.Errorf("expected error to name the tool 'write_file', got: %v", err)
	}
}

// TestFilterToolsByPermissions_NilSafe documents the nil-safe paths on
// the helper added by this ticket: a nil toolclient, an empty agentID, or
// an empty tool slice all short-circuit to the input. This matters
// because SelectForAgent can run with a nil toolclient on certain test
// paths (TestToolService_SelectForAgent_NoTools) — the helper must not
// panic.
func TestFilterToolsByPermissions_NilSafe(t *testing.T) {
	tools := []llmtypes.ToolDefinition{{Name: "anything"}}

	if got := filterToolsByPermissions(nil, "agent", tools); len(got) != 1 {
		t.Errorf("nil toolclient: expected pass-through, got %d tools", len(got))
	}

	tc := buildToolClientForPerms(t)
	if got := filterToolsByPermissions(tc, "", tools); len(got) != 1 {
		t.Errorf("empty agentID: expected pass-through, got %d tools", len(got))
	}
	if got := filterToolsByPermissions(tc, "agent", nil); len(got) != 0 {
		t.Errorf("nil tools: expected empty output, got %d tools", len(got))
	}
}

// TestFilterToolsByPermissions_FiltersDiscoveryFallback exercises the
// gap this ticket closed: tools that arrive via the discoverAgentMCPTools
// fallback (or any other code path that bypasses SelectToolsAsProvider's
// builtin/broker filter loops) must still be subject to the policy. The
// helper is the gate.
func TestFilterToolsByPermissions_FiltersDiscoveryFallback(t *testing.T) {
	const agentID = "agent-readonly"

	tc := buildToolClientForPerms(t)
	tc.PermissionResolver = permissionResolverFor(agentID, toolclient.ToolPermissions{
		AllowList:       []string{"read_file"},
		MaxCallsPerTurn: 25,
	})

	// Simulate the post-discovery state: an arbitrary tool set the
	// fallback path might emit, including a forbidden one.
	tools := []llmtypes.ToolDefinition{
		{Name: "read_file"},
		{Name: "write_file"}, // forbidden by allow_list
		{Name: "list_files"}, // forbidden by allow_list (not in list)
	}

	got := sortedToolNames(filterToolsByPermissions(tc, agentID, tools))
	want := []string{"read_file"}
	if !sliceEq(got, want) {
		t.Errorf("filterToolsByPermissions = %v, want %v", got, want)
	}
}
