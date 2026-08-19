package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// createTestAgentForGrant creates a real, DB-backed (real UUID identity)
// managed agent via the REST layer. TASKS/adhoc/01-eliminate-file-based-
// agent-runtime.md removed the requireRealAgentToolsTarget guard this
// comment used to reference -- every agent (managed or internal/embedded)
// now carries a real UUID, so this helper's shape is no longer load-bearing
// for that distinction, just a convenient way to seed a fixture agent.
func createTestAgentForGrant(t *testing.T, mux http.Handler, slug string, extra map[string]any) store.AgentProfile {
	t.Helper()
	body := map[string]any{
		"name":          "Grant Test " + slug,
		"slug":          slug,
		"system_prompt": "test",
	}
	for k, v := range extra {
		body[k] = v
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/agents", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /api/agents (setup %s): expected 201, got %d; body: %s", slug, w.Code, w.Body.String())
	}
	var created store.AgentProfile
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created agent: %v", err)
	}
	return created
}

// TestAgentToolsGrantRevoke_EndToEnd is the integration test required by
// TASKS/phase-5/01-build-assignment-api.md's Done-means bullet 3:
// agent_tools grant/revoke works via the API and correctly rejects a grant
// referencing a nonexistent known_tools row or a nonexistent agent.
func TestAgentToolsGrantRevoke_EndToEnd(t *testing.T) {
	a, mux := newTestAPI(t)
	ctx := context.Background()

	agent := createTestAgentForGrant(t, mux, "grant-revoke-agent", nil)

	toolID, err := a.Services.Store.UpsertKnownTool(ctx, "dev_read", "builtin", "available", "")
	if err != nil {
		t.Fatalf("UpsertKnownTool: %v", err)
	}

	// POST /api/agents/{id}/tools -- grant.
	grantBody, _ := json.Marshal(GrantAgentToolRequest{ToolID: toolID})
	req := httptest.NewRequest("POST", "/api/agents/"+agent.ID+"/tools", bytes.NewReader(grantBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /api/agents/{id}/tools: expected 201, got %d; body: %s", w.Code, w.Body.String())
	}
	var grantResp struct {
		ToolNames []string `json:"tool_names"`
	}
	if err := json.NewDecoder(w.Body).Decode(&grantResp); err != nil {
		t.Fatalf("decode grant response: %v", err)
	}
	if len(grantResp.ToolNames) != 1 || grantResp.ToolNames[0] != "dev_read" {
		t.Fatalf("grant response tool_names = %v, want [dev_read]", grantResp.ToolNames)
	}

	names, err := a.Services.Store.ListAgentToolNames(ctx, agent.ID)
	if err != nil {
		t.Fatalf("ListAgentToolNames: %v", err)
	}
	if len(names) != 1 || names[0] != "dev_read" {
		t.Fatalf("ListAgentToolNames after grant: got %v, want [dev_read]", names)
	}

	// DELETE /api/agents/{id}/tools/{toolId} -- revoke.
	req = httptest.NewRequest("DELETE", "/api/agents/"+agent.ID+"/tools/"+toolID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE /api/agents/{id}/tools/{toolId}: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	names, err = a.Services.Store.ListAgentToolNames(ctx, agent.ID)
	if err != nil {
		t.Fatalf("ListAgentToolNames after revoke: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("ListAgentToolNames after revoke: got %v, want []", names)
	}
}

// TestHandleGrantAgentTool_RejectsNonexistentTool pins the FK-integrity
// discipline this task's Done-means calls for (same as Phase 1 task 05):
// granting a tool_id with no matching known_tools row must 404, not insert
// a dangling agent_tools row.
func TestHandleGrantAgentTool_RejectsNonexistentTool(t *testing.T) {
	a, mux := newTestAPI(t)
	agent := createTestAgentForGrant(t, mux, "grant-bad-tool-agent", nil)

	grantBody, _ := json.Marshal(GrantAgentToolRequest{ToolID: "does-not-exist"})
	req := httptest.NewRequest("POST", "/api/agents/"+agent.ID+"/tools", bytes.NewReader(grantBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("POST /api/agents/{id}/tools with bad tool_id: expected 404, got %d; body: %s", w.Code, w.Body.String())
	}

	names, err := a.Services.Store.ListAgentToolNames(context.Background(), agent.ID)
	if err != nil {
		t.Fatalf("ListAgentToolNames: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("expected no agent_tools row for a rejected grant, got %v", names)
	}
}

// TestHandleGrantAgentTool_RejectsNonexistentAgent mirrors
// TestHandleAddAgentProject_RejectsNonexistentAgent's precedent
// (agent_projects_test.go, Phase 1 #05) for this endpoint.
func TestHandleGrantAgentTool_RejectsNonexistentAgent(t *testing.T) {
	a, mux := newTestAPI(t)
	ctx := context.Background()

	toolID, err := a.Services.Store.UpsertKnownTool(ctx, "dev_read", "builtin", "available", "")
	if err != nil {
		t.Fatalf("UpsertKnownTool: %v", err)
	}

	grantBody, _ := json.Marshal(GrantAgentToolRequest{ToolID: toolID})
	req := httptest.NewRequest("POST", "/api/agents/does-not-exist/tools", bytes.NewReader(grantBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("POST /api/agents/{missing}/tools: expected 404, got %d; body: %s", w.Code, w.Body.String())
	}
}

// TestHandleGrantAgentTool_SucceedsDespiteStaleDenyList is the direct
// replacement for the deleted TestHandleGrantAgentTool_RejectsStaleDenyListMatch
// (TASKS/phase-5/01's stale-legacy-deny-list footgun test): that test
// asserted a grant was REJECTED (409) when the agent's legacy
// tool_permissions.deny_list still matched the granted tool, because
// tool_permissions.CheckPermission stayed live as a deeper broker-level
// backstop that could silently veto the grant. TASKS/adhoc/02-remove-
// tool-permissions-collapse-to-agent-tools.md removed that backstop
// entirely, so the same setup must now succeed (201) -- there is no more
// deeper mechanism left for a stale deny_list to conflict with.
func TestHandleGrantAgentTool_SucceedsDespiteStaleDenyList(t *testing.T) {
	a, mux := newTestAPI(t)
	ctx := context.Background()

	agent := createTestAgentForGrant(t, mux, "grant-deny-list-agent", map[string]any{
		"tool_permissions": `{"deny_list":["dev_*"]}`,
	})
	if agent.ToolPermissions == "" {
		t.Fatalf("setup: expected tool_permissions to be set, got empty")
	}

	toolID, err := a.Services.Store.UpsertKnownTool(ctx, "dev_read", "builtin", "available", "")
	if err != nil {
		t.Fatalf("UpsertKnownTool: %v", err)
	}

	grantBody, _ := json.Marshal(GrantAgentToolRequest{ToolID: toolID})
	req := httptest.NewRequest("POST", "/api/agents/"+agent.ID+"/tools", bytes.NewReader(grantBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /api/agents/{id}/tools: expected 201 (tool_permissions no longer vetoes grants), got %d; body: %s", w.Code, w.Body.String())
	}

	names, err := a.Services.Store.ListAgentToolNames(ctx, agent.ID)
	if err != nil {
		t.Fatalf("ListAgentToolNames: %v", err)
	}
	if len(names) != 1 || names[0] != "dev_read" {
		t.Errorf("expected dev_read to be granted despite the stale deny_list, got %v", names)
	}
}
