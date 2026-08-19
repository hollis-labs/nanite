package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// TestHandleListAgentTools_DBBackedAgentUsesAgentTools is the Done-means
// regression test for TASKS/phase-5/10-fix-list-agent-tools-endpoint-stale-
// permissions-view.md: a real agent_profiles-backed agent with exactly one
// agent_tools grant must see that tool as allowed:true and every other
// catalog tool as allowed:false -- reproducing and closing the live-verified
// gap in that task's Context (the endpoint used to read the legacy
// tool_permissions path, which reports "everything allowed" for an agent
// with no tool_permissions configured, independent of agent_tools).
func TestHandleListAgentTools_DBBackedAgentUsesAgentTools(t *testing.T) {
	a, mux := newTestAPI(t)
	ctx := context.Background()

	// Wire a ToolClient with a small builtin catalog -- newTestAPI's
	// container doesn't wire one by default (no MCP/config in this
	// lightweight harness).
	tc := toolclient.New(mcp.NewManager(), a.Services.Store, nil)
	tc.Builtins.RegisterBuiltins("dev", []llmtypes.ToolDefinition{
		{Name: "dev_read", Description: "Read a file"},
		{Name: "dev_write", Description: "Write a file"},
		{Name: "dev_bash", Description: "Run a shell command"},
	})
	a.Services.ToolClient = tc

	agent := createTestAgentForGrant(t, mux, "list-tools-db-agent", nil)

	toolID, err := a.Services.Store.UpsertKnownTool(ctx, "dev_read", "builtin", "available", "")
	if err != nil {
		t.Fatalf("UpsertKnownTool: %v", err)
	}
	if err := a.Services.Store.GrantAgentTool(ctx, agent.ID, toolID, "explicit"); err != nil {
		t.Fatalf("GrantAgentTool: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/agents/"+agent.ID+"/tools", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/agents/{id}/tools: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var items []struct {
		Name    string `json:"name"`
		Allowed bool   `json:"allowed"`
	}
	if err := json.NewDecoder(w.Body).Decode(&items); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 tools in catalog, got %d: %+v", len(items), items)
	}

	got := make(map[string]bool, len(items))
	for _, it := range items {
		got[it.Name] = it.Allowed
	}
	if !got["dev_read"] {
		t.Errorf("expected dev_read (the sole agent_tools grant) to be allowed:true, got %v", got)
	}
	if got["dev_write"] {
		t.Errorf("expected dev_write (no agent_tools grant) to be allowed:false, got %v", got)
	}
	if got["dev_bash"] {
		t.Errorf("expected dev_bash (no agent_tools grant) to be allowed:false, got %v", got)
	}
}

// TestHandleListAgentTools_FileBasedAgentUnchanged is the regression check
// (Done-means bullet 2): an agentID with no real agent_profiles row (the
// file-based population, which cannot hold agent_tools grants) must keep
// reading its allowed flags from the legacy tool_permissions/CheckPermission
// path, unaffected by this task's dbAgent-resolution fix.
func TestHandleListAgentTools_FileBasedAgentUnchanged(t *testing.T) {
	a, mux := newTestAPI(t)

	tc := toolclient.New(mcp.NewManager(), a.Services.Store, nil)
	tc.Builtins.RegisterBuiltins("dev", []llmtypes.ToolDefinition{
		{Name: "dev_read", Description: "Read a file"},
		{Name: "dev_write", Description: "Write a file"},
	})
	// Simulate the file-based agent's PermissionResolver path
	// (newFileAgentPermissionResolver in production) with a restrictive
	// allow-list -- distinguishes this test from the default-permit
	// fallback GetPermissions would otherwise apply for an unresolvable ID.
	tc.PermissionResolver = func(agentID string) (toolclient.ToolPermissions, bool) {
		return toolclient.ToolPermissions{AllowList: []string{"dev_read"}}, true
	}
	a.Services.ToolClient = tc

	// "file-worker" has no agent_profiles row -- store.GetAgent misses,
	// so handleListAgentTools must fall back to the legacy path.
	req := httptest.NewRequest("GET", "/api/agents/file-worker/tools", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/agents/{id}/tools: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var items []struct {
		Name    string `json:"name"`
		Allowed bool   `json:"allowed"`
	}
	if err := json.NewDecoder(w.Body).Decode(&items); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	got := make(map[string]bool, len(items))
	for _, it := range items {
		got[it.Name] = it.Allowed
	}
	if !got["dev_read"] {
		t.Errorf("expected dev_read (in tool_permissions.allow_list) to be allowed:true, got %v", got)
	}
	if got["dev_write"] {
		t.Errorf("expected dev_write (not in tool_permissions.allow_list) to be allowed:false, got %v", got)
	}
}
