package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/toolclient"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

// Historical raw grants remain visible to export but cannot grant current
// catalog membership. The read-only catalog still reports all live tool names.
func TestHandleListAgentTools_HistoricalGrantDoesNotAuthorizeCatalog(t *testing.T) {
	a, mux := newTestAPI(t)
	tc := toolclient.New(mcp.NewManager(), a.store, nil)
	tc.Builtins.RegisterBuiltins("dev", []llmtypes.ToolDefinition{{Name: "dev_read", Description: "Read a file"}, {Name: "dev_write", Description: "Write a file"}, {Name: "dev_bash", Description: "Run a shell command"}})
	a.Services.ToolClient = tc
	p := historicalToolGrantFixture(t, a, "list-tools-db-agent")
	toolID, err := a.store.UpsertKnownTool(t.Context(), "dev_read", "builtin", "available", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.DB.ExecContext(t.Context(), `INSERT INTO agent_tools(agent_id,tool_id,granted_via,created_at) VALUES(?,?,'explicit','retained-created')`, p.ID, toolID); err != nil {
		t.Fatal(err)
	}
	const query = `SELECT * FROM agent_tools ORDER BY agent_id,tool_id`
	before := retiredAPISnapshot(t, a, query)
	w := retiredAPIRequest(t, mux, "GET", "/api/agents/"+p.ID+"/tools", "")
	if w.Code != http.StatusOK {
		t.Fatalf("read-only tool catalog: %d %s", w.Code, w.Body.String())
	}
	var items []struct {
		Name    string `json:"name"`
		Allowed bool   `json:"allowed"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("live tool catalog lost entries: %+v", items)
	}
	got := make(map[string]bool, len(items))
	for _, item := range items {
		got[item.Name] = item.Allowed
	}
	for _, name := range []string{"dev_read", "dev_write", "dev_bash"} {
		allowed, ok := got[name]
		if !ok || allowed {
			t.Fatalf("historical grant authorized %s or catalog lost it: %v", name, got)
		}
	}
	retiredAPIHistoryUnchanged(t, a, query, before)
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

	tc := toolclient.New(mcp.NewManager(), a.store, nil)
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

	tc := toolclient.New(mcp.NewManager(), a.store, nil)
	tc.Builtins.RegisterBuiltins("dev", []llmtypes.ToolDefinition{
		{Name: "dev_read", Description: "Read a file"},
		{Name: "dev_unsynced", Description: "Not yet in known_tools"},
	})
	a.Services.ToolClient = tc

	agent := historicalToolGrantFixture(t, a, "list-tools-id-agent")

	toolID, err := a.store.UpsertKnownTool(ctx, "dev_read", "builtin", "available", "")
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

// fakeMCPServerTransport is a minimal mcp.MCPTransport for wiring a fake MCP
// server into the manager's uniformIndex via DiscoverTools, so
// Manager.ToolAttribution resolves real (server, ok) pairs without a real
// subprocess/HTTP connection.
type fakeMCPServerTransport struct {
	tools []mcp.Tool
}

func (f *fakeMCPServerTransport) ListTools(context.Context) ([]mcp.Tool, error) {
	return f.tools, nil
}

func (f *fakeMCPServerTransport) CallTool(context.Context, string, map[string]any) (*mcp.ToolResult, error) {
	return nil, nil
}

// TestHandleListAgentTools_ReportsMCPServerOrigin is the Done-means test for
// CW-20260918-0020: a tool resolved from a registered MCP server reports
// source:"mcp" and mcp_server:"<the server's name>" so a caller (e.g.
// Tachyon's Agent Ops plugin) can filter by real server identity instead of
// a name-prefix heuristic; a builtin tool with no MCP registration reports
// source:"builtin" and no mcp_server.
func TestHandleListAgentTools_ReportsMCPServerOrigin(t *testing.T) {
	a, mux := newTestAPI(t)
	ctx := context.Background()

	mgr := mcp.NewManager()
	if err := mgr.AddServer("Agent Mux", &fakeMCPServerTransport{
		tools: []mcp.Tool{{Name: "mux_search", Description: "Search via Agent Mux"}},
	}, mcp.TierThirdPartyHTTP); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	if err := mgr.DiscoverTools(ctx); err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}

	tc := toolclient.New(mgr, a.store, nil)
	tc.Builtins.RegisterBuiltins("dev", []llmtypes.ToolDefinition{
		{Name: "dev_read", Description: "Read a file"},
	})
	a.Services.ToolClient = tc
	a.Services.MCP = mgr

	agent := historicalToolGrantFixture(t, a, "list-tools-origin-agent")

	req := httptest.NewRequest("GET", "/api/agents/"+agent.ID+"/tools", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/agents/{id}/tools: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var items []struct {
		Name      string `json:"name"`
		Source    string `json:"source"`
		MCPServer string `json:"mcp_server"`
	}
	if err := json.NewDecoder(w.Body).Decode(&items); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	byName := make(map[string]struct {
		Source    string
		MCPServer string
	}, len(items))
	for _, it := range items {
		byName[it.Name] = struct {
			Source    string
			MCPServer string
		}{it.Source, it.MCPServer}
	}
	if got := byName["mux_search"]; got.Source != "mcp" || got.MCPServer != "Agent Mux" {
		t.Errorf("expected mux_search source=mcp mcp_server=%q, got source=%q mcp_server=%q", "Agent Mux", got.Source, got.MCPServer)
	}
	if got := byName["dev_read"]; got.Source != "builtin" || got.MCPServer != "" {
		t.Errorf("expected dev_read source=builtin mcp_server=\"\", got source=%q mcp_server=%q", got.Source, got.MCPServer)
	}
}
