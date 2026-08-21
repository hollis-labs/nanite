package api

// TASKS/agent-host-acp/11-nanite-per-agent-protocol-transport-config.md:
// live REST CRUD tests for agent_profiles.protocol/transport, mirroring
// agents_composition_test.go's RoleID/ConsumerID/ModelID test shapes (the
// closest existing precedent, per this task's own "add CRUD following
// whatever the closest existing precedent is").

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestHandleCreateAgent_SetsProtocolTransport is the Done-means live
// integration test: protocol/transport are settable via POST /api/agents
// and correctly readable back afterward, independent of the POST response.
func TestHandleCreateAgent_SetsProtocolTransport(t *testing.T) {
	a, mux := newTestAPI(t)

	body, _ := json.Marshal(map[string]any{
		"name":          "ACP Agent",
		"slug":          "acp-agent",
		"system_prompt": "You are an ACP-driven agent.",
		"protocol":      "acp",
		"transport":     "tcp",
	})
	req := httptest.NewRequest("POST", "/api/agents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /api/agents: expected 201, got %d; body: %s", w.Code, w.Body.String())
	}

	var created store.AgentProfile
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created agent: %v", err)
	}
	if created.Protocol != "acp" {
		t.Errorf("created.Protocol = %q, want acp", created.Protocol)
	}
	if created.Transport != "tcp" {
		t.Errorf("created.Transport = %q, want tcp", created.Transport)
	}
	// No new runtime_kind value -- confirmed against the real API response,
	// not just the store layer.
	if created.RuntimeKind != "cli" && created.RuntimeKind != "api" {
		t.Errorf("created.RuntimeKind = %q, want an existing value ('cli' or 'api')", created.RuntimeKind)
	}

	req = httptest.NewRequest("GET", "/api/agents/"+created.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/agents/{id}: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var getResp struct {
		Agent store.AgentProfile `json:"agent"`
	}
	if err := json.NewDecoder(w.Body).Decode(&getResp); err != nil {
		t.Fatalf("decode get agent: %v", err)
	}
	if getResp.Agent.Protocol != "acp" || getResp.Agent.Transport != "tcp" {
		t.Errorf("GET after create: protocol/transport did not round-trip: %+v", getResp.Agent)
	}
	_ = a
}

// TestHandleCreateAgent_InvalidProtocolRejected mirrors
// TestHandleCreateAgent_InvalidRoleIDRejected's FK-integrity-style
// discipline for the enum case: an unrecognized protocol value must fail
// the request, not silently insert a value the Go/DB layer can't dispatch.
func TestHandleCreateAgent_InvalidProtocolRejected(t *testing.T) {
	_, mux := newTestAPI(t)

	body, _ := json.Marshal(map[string]any{
		"name":          "Bad Protocol Agent",
		"slug":          "bad-protocol-agent",
		"system_prompt": "test",
		"protocol":      "made-up-protocol",
	})
	req := httptest.NewRequest("POST", "/api/agents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/agents with invalid protocol: expected 400, got %d; body: %s", w.Code, w.Body.String())
	}
}

// TestHandleUpdateAgent_SetsAndClearsProtocolTransport exercises the
// pointer-partial-update semantics on the PUT path, and confirms an
// unrelated field-only edit does not disturb protocol/transport (the same
// reingest-preservation property TestHandleUpdateAgent_
// SetsAndClearsCompositionFields pins for role_id).
func TestHandleUpdateAgent_SetsAndClearsProtocolTransport(t *testing.T) {
	_, mux := newTestAPI(t)

	createBody, _ := json.Marshal(map[string]any{
		"name":          "Update ACP Agent",
		"slug":          "update-acp-agent",
		"system_prompt": "test",
	})
	req := httptest.NewRequest("POST", "/api/agents", bytes.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /api/agents: expected 201, got %d; body: %s", w.Code, w.Body.String())
	}
	var created store.AgentProfile
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created agent: %v", err)
	}
	if created.Protocol != "" {
		t.Fatalf("setup: expected Protocol empty on create, got %q", created.Protocol)
	}

	// Set protocol via PUT.
	updateBody, _ := json.Marshal(map[string]any{"protocol": "acp"})
	req = httptest.NewRequest("PUT", "/api/agents/"+created.ID, bytes.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /api/agents/{id} (set protocol): expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var updated store.AgentProfile
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated agent: %v", err)
	}
	if updated.Protocol != "acp" {
		t.Fatalf("after set: Protocol = %q, want acp", updated.Protocol)
	}

	// An unrelated field-only edit must not disturb protocol/transport.
	unrelatedBody, _ := json.Marshal(map[string]any{"description": "unrelated edit"})
	req = httptest.NewRequest("PUT", "/api/agents/"+created.ID, bytes.NewReader(unrelatedBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /api/agents/{id} (unrelated edit): expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var afterUnrelated store.AgentProfile
	if err := json.NewDecoder(w.Body).Decode(&afterUnrelated); err != nil {
		t.Fatalf("decode agent after unrelated edit: %v", err)
	}
	if afterUnrelated.Protocol != "acp" {
		t.Fatalf("an unrelated field edit wiped Protocol: got %q, want acp", afterUnrelated.Protocol)
	}

	// Clear protocol via PUT with an explicit empty string.
	clearBody, _ := json.Marshal(map[string]any{"protocol": ""})
	req = httptest.NewRequest("PUT", "/api/agents/"+created.ID, bytes.NewReader(clearBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /api/agents/{id} (clear protocol): expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var cleared store.AgentProfile
	if err := json.NewDecoder(w.Body).Decode(&cleared); err != nil {
		t.Fatalf("decode cleared agent: %v", err)
	}
	if cleared.Protocol != "" {
		t.Fatalf("after clear: Protocol = %q, want empty (native)", cleared.Protocol)
	}
}

// TestHandleUpdateAgent_ExistingAgentsUnaffected confirms an agent created
// through the normal flow with no protocol/transport opinion at all keeps
// behaving exactly as before after an unrelated update -- the Done-means
// "every existing agent's behavior is unchanged" requirement, exercised
// against the real API surface.
func TestHandleUpdateAgent_ExistingAgentsUnaffected(t *testing.T) {
	_, mux := newTestAPI(t)

	createBody, _ := json.Marshal(map[string]any{
		"name":          "Plain Agent",
		"slug":          "plain-agent",
		"system_prompt": "test",
	})
	req := httptest.NewRequest("POST", "/api/agents", bytes.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /api/agents: expected 201, got %d; body: %s", w.Code, w.Body.String())
	}
	var created store.AgentProfile
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created agent: %v", err)
	}
	if created.Protocol != "" || created.Transport != "" {
		t.Fatalf("a plain agent with no protocol/transport opinion must default to empty (native); got %q/%q", created.Protocol, created.Transport)
	}

	unrelatedBody, _ := json.Marshal(map[string]any{"description": "totally unrelated"})
	req = httptest.NewRequest("PUT", "/api/agents/"+created.ID, bytes.NewReader(unrelatedBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /api/agents/{id}: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var after store.AgentProfile
	if err := json.NewDecoder(w.Body).Decode(&after); err != nil {
		t.Fatalf("decode agent after unrelated edit: %v", err)
	}
	if after.Protocol != "" || after.Transport != "" {
		t.Fatalf("an unrelated edit must not introduce a protocol/transport opinion; got %q/%q", after.Protocol, after.Transport)
	}
}
