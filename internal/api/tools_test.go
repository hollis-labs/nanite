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

// TestHandleListAgentTools_UnknownAgentDeniesAll is the regression check
// for TASKS/adhoc/02-remove-tool-permissions-collapse-to-agent-tools.md's
// unconditional agent_tools collapse: an agentID with no real
// agent_profiles row (previously the file-based population, eliminated by
// TASKS/adhoc/01, or simply a typo'd/unknown ID) reads back zero
// agent_tools grants -- fail closed (allowed:false for everything), NOT
// the legacy tool_permissions/CheckPermission fallback's "everything
// allowed" default this replaced.
func TestHandleListAgentTools_UnknownAgentDeniesAll(t *testing.T) {
	a, mux := newTestAPI(t)

	tc := toolclient.New(mcp.NewManager(), a.Services.Store, nil)
	tc.Builtins.RegisterBuiltins("dev", []llmtypes.ToolDefinition{
		{Name: "dev_read", Description: "Read a file"},
		{Name: "dev_write", Description: "Write a file"},
	})
	a.Services.ToolClient = tc

	req := httptest.NewRequest("GET", "/api/agents/does-not-exist/tools", nil)
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
	for _, it := range items {
		if it.Allowed {
			t.Errorf("expected every tool to be allowed:false for an unknown agentID, got %s=true", it.Name)
		}
	}
}

// TestHandleListAgentTools_ResolvesKnownToolID pins the `id` field this
// endpoint gained so a UI can grant/revoke directly off its response
// without a second lookup: a tool synced into known_tools resolves its
// row ID, and a live-catalog tool that hasn't been synced yet (the
// "known_tools learns MCP tools only at boot" gap docs/adding-an-agent.md
// describes) reports id:"" rather than a wrong or panicking lookup.
func TestHandleListAgentTools_ResolvesKnownToolID(t *testing.T) {
	a, mux := newTestAPI(t)
	ctx := context.Background()

	tc := toolclient.New(mcp.NewManager(), a.Services.Store, nil)
	tc.Builtins.RegisterBuiltins("dev", []llmtypes.ToolDefinition{
		{Name: "dev_read", Description: "Read a file"},
		{Name: "dev_unsynced", Description: "Not yet in known_tools"},
	})
	a.Services.ToolClient = tc

	agent := createTestAgentForGrant(t, mux, "list-tools-id-agent", nil)

	toolID, err := a.Services.Store.UpsertKnownTool(ctx, "dev_read", "builtin", "available", "")
	if err != nil {
		t.Fatalf("UpsertKnownTool: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/agents/"+agent.ID+"/tools", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/agents/{id}/tools: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var items []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(w.Body).Decode(&items); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	byName := make(map[string]string, len(items))
	for _, it := range items {
		byName[it.Name] = it.ID
	}
	if byName["dev_read"] != toolID {
		t.Errorf("expected dev_read id=%q (its known_tools row), got %q", toolID, byName["dev_read"])
	}
	if byName["dev_unsynced"] != "" {
		t.Errorf("expected dev_unsynced (never synced into known_tools) to report id=\"\", got %q", byName["dev_unsynced"])
	}
}
