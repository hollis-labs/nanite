package api

// Phase 2 item 02 (TASKS/phase-2/02-port-forward-dynamic-resolver.md).

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestAgentContextResolversAPI_CRUD_EndToEnd(t *testing.T) {
	a, mux := newTestAPI(t)
	agent := &store.AgentProfile{Name: "Resolver Agent", Slug: "resolver-agent", SystemPrompt: "x", Class: "advisor"}
	if err := a.Services.Store.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// POST /api/agents/{id}/context-resolvers
	createBody, _ := json.Marshal(CreateAgentContextResolverRequest{
		SlotName: "weather",
		Kind:     "cmd",
		Run:      "printf 72F",
		Timeout:  "5s",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/agents/"+agent.ID+"/context-resolvers", bytes.NewReader(createBody))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST context-resolvers: expected 201, got %d; body: %s", w.Code, w.Body.String())
	}
	var created store.AgentContextResolver
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created resolver: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected resolver ID to be set")
	}
	if !created.Enabled {
		t.Error("expected Enabled to default true when omitted from the request")
	}

	// A malformed create (bad kind) is rejected with 400.
	badBody, _ := json.Marshal(CreateAgentContextResolverRequest{SlotName: "bad", Kind: "role_summary"})
	req = httptest.NewRequest(http.MethodPost, "/api/agents/"+agent.ID+"/context-resolvers", bytes.NewReader(badBody))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST context-resolvers (bad kind): expected 400, got %d; body: %s", w.Code, w.Body.String())
	}

	// GET /api/agents/{id}/context-resolvers
	req = httptest.NewRequest(http.MethodGet, "/api/agents/"+agent.ID+"/context-resolvers", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET context-resolvers: expected 200, got %d", w.Code)
	}
	var list []store.AgentContextResolver
	if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 resolver, got %d", len(list))
	}

	// GET /api/agents/{id}/context-resolvers/{resolverId}
	req = httptest.NewRequest(http.MethodGet, "/api/agents/"+agent.ID+"/context-resolvers/"+created.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET context-resolvers/{id}: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// GET for a nonexistent id -> 404.
	req = httptest.NewRequest(http.MethodGet, "/api/agents/"+agent.ID+"/context-resolvers/does-not-exist", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET context-resolvers/{missing}: expected 404, got %d", w.Code)
	}

	// PATCH /api/agents/{id}/context-resolvers/{resolverId}
	patchBody, _ := json.Marshal(map[string]any{"run": "printf 80F", "enabled": false})
	req = httptest.NewRequest(http.MethodPatch, "/api/agents/"+agent.ID+"/context-resolvers/"+created.ID, bytes.NewReader(patchBody))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH context-resolvers/{id}: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var updated store.AgentContextResolver
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated resolver: %v", err)
	}
	if updated.Run != "printf 80F" || updated.Enabled {
		t.Errorf("PATCH did not apply: %+v", updated)
	}
	// Untouched fields survive the partial update.
	if updated.SlotName != "weather" {
		t.Errorf("partial update should not have touched slot_name: got %q", updated.SlotName)
	}

	// A disabled resolver drops out of the boot-time enabled listing.
	enabled, err := a.Services.Store.ListEnabledAgentContextResolvers(req.Context(), agent.ID)
	if err != nil {
		t.Fatalf("ListEnabledAgentContextResolvers: %v", err)
	}
	if len(enabled) != 0 {
		t.Fatalf("expected 0 enabled resolvers after disabling, got %d", len(enabled))
	}

	// DELETE /api/agents/{id}/context-resolvers/{resolverId}
	req = httptest.NewRequest(http.MethodDelete, "/api/agents/"+agent.ID+"/context-resolvers/"+created.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE context-resolvers/{id}: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/agents/"+agent.ID+"/context-resolvers/"+created.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET context-resolvers/{id} after delete: expected 404, got %d", w.Code)
	}
}

// TestAgentContextResolversAPI_CrossAgentAccessRejected ensures a resolver
// created under one agent can't be patched or deleted through a different
// agent's path segment -- mirrors handlePatchAgentReflex /
// handleDeleteAgentReflex's own cross-agent guard.
func TestAgentContextResolversAPI_CrossAgentAccessRejected(t *testing.T) {
	a, mux := newTestAPI(t)
	agentA := &store.AgentProfile{Name: "Agent A", Slug: "resolver-agent-a", SystemPrompt: "x", Class: "advisor"}
	agentB := &store.AgentProfile{Name: "Agent B", Slug: "resolver-agent-b", SystemPrompt: "x", Class: "advisor"}
	if err := a.Services.Store.CreateAgent(agentA); err != nil {
		t.Fatalf("CreateAgent A: %v", err)
	}
	if err := a.Services.Store.CreateAgent(agentB); err != nil {
		t.Fatalf("CreateAgent B: %v", err)
	}

	createBody, _ := json.Marshal(CreateAgentContextResolverRequest{SlotName: "weather", Kind: "cmd", Run: "printf hi"})
	req := httptest.NewRequest(http.MethodPost, "/api/agents/"+agentA.ID+"/context-resolvers", bytes.NewReader(createBody))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST context-resolvers: expected 201, got %d; body: %s", w.Code, w.Body.String())
	}
	var created store.AgentContextResolver
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created resolver: %v", err)
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/agents/"+agentB.ID+"/context-resolvers/"+created.ID, bytes.NewReader([]byte(`{"run":"printf bye"}`)))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("PATCH cross-agent: expected 400, got %d; body: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/agents/"+agentB.ID+"/context-resolvers/"+created.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("DELETE cross-agent: expected 400, got %d; body: %s", w.Code, w.Body.String())
	}
}
