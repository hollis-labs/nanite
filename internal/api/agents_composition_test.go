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

// seedTestModel inserts a minimal providers + models row directly via SQL
// (the store package has no CreateModel/CreateProvider write helper this
// test can call -- models are normally synced from models.dev, and
// providers are seeded via config, neither of which this lightweight test
// DB has). Returns the minted model row's id (the models.id PK,
// agent_profiles.model_id's FK target -- distinct from models.model_id,
// the wire model identifier string).
func seedTestModel(t *testing.T, a *API) string {
	t.Helper()
	if _, err := a.Services.Store.DB.Exec(
		`INSERT INTO providers (id, name, provider_type) VALUES (?, ?, ?)`,
		"prov-composition-test", "Test Provider", "anthropic",
	); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	modelID := "model-composition-test"
	if _, err := a.Services.Store.DB.Exec(
		`INSERT INTO models (id, provider_id, model_id, display_name) VALUES (?, ?, ?, ?)`,
		modelID, "prov-composition-test", "claude-test-model", "Claude Test Model",
	); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	return modelID
}

// TestHandleCreateAgent_SetsCompositionFields is the Done-means live
// integration test for TASKS/phase-5/01-build-assignment-api.md's first
// bullet: role_id/consumer_id/model_id are settable via POST /api/agents
// and correctly readable back afterward.
func TestHandleCreateAgent_SetsCompositionFields(t *testing.T) {
	a, mux := newTestAPI(t)

	role := &store.Role{Slug: "composition-role", Name: "Composition Role", SystemPrompt: "You help."}
	if err := a.Services.Store.CreateRole(context.Background(), role); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	modelID := seedTestModel(t, a)
	// Migration 112 seeds a real 'blt-loom-001' consumer row -- reuse it
	// instead of adding store-layer CRUD-test setup this file doesn't need.
	const consumerID = "blt-loom-001"

	body, _ := json.Marshal(map[string]any{
		"name":          "Composition Agent",
		"slug":          "composition-agent",
		"system_prompt": "You are a composed agent.",
		"role_id":       role.ID,
		"consumer_id":   consumerID,
		"model_id":      modelID,
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
	if created.RoleID != role.ID {
		t.Errorf("created.RoleID = %q, want %q", created.RoleID, role.ID)
	}
	if created.ConsumerID != consumerID {
		t.Errorf("created.ConsumerID = %q, want %q", created.ConsumerID, consumerID)
	}
	if created.ModelID != modelID {
		t.Errorf("created.ModelID = %q, want %q", created.ModelID, modelID)
	}

	// Read back via GET, independent of the POST response, to prove this
	// persisted to the row and isn't just reflected in the create response.
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
	if getResp.Agent.RoleID != role.ID || getResp.Agent.ConsumerID != consumerID || getResp.Agent.ModelID != modelID {
		t.Errorf("GET after create: composition fields did not round-trip: %+v", getResp.Agent)
	}
}

// TestHandleCreateAgent_InvalidRoleIDRejected pins the FK-integrity
// discipline this task's Done-means calls for: a composition write
// referencing a nonexistent roles row must fail, not silently insert a
// dangling reference.
func TestHandleCreateAgent_InvalidRoleIDRejected(t *testing.T) {
	_, mux := newTestAPI(t)

	body, _ := json.Marshal(map[string]any{
		"name":          "Bad Role Agent",
		"slug":          "bad-role-agent",
		"system_prompt": "test",
		"role_id":       "does-not-exist",
	})
	req := httptest.NewRequest("POST", "/api/agents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/agents with invalid role_id: expected 400, got %d; body: %s", w.Code, w.Body.String())
	}
}

// TestHandleUpdateAgent_SetsAndClearsCompositionFields exercises the
// pointer-partial-update semantics on the PUT path: a nil field leaves the
// column untouched, and a pointer to "" clears an already-set FK back to
// NULL.
func TestHandleUpdateAgent_SetsAndClearsCompositionFields(t *testing.T) {
	a, mux := newTestAPI(t)

	role := &store.Role{Slug: "composition-role-2", Name: "Composition Role 2", SystemPrompt: "You help."}
	if err := a.Services.Store.CreateRole(context.Background(), role); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	createBody, _ := json.Marshal(map[string]any{
		"name":          "Update Composition Agent",
		"slug":          "update-composition-agent",
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
	if created.RoleID != "" {
		t.Fatalf("setup: expected RoleID empty on create, got %q", created.RoleID)
	}

	// Set role_id via PUT.
	updateBody, _ := json.Marshal(map[string]any{"role_id": role.ID})
	req = httptest.NewRequest("PUT", "/api/agents/"+created.ID, bytes.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /api/agents/{id} (set role_id): expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var updated store.AgentProfile
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated agent: %v", err)
	}
	if updated.RoleID != role.ID {
		t.Fatalf("after set: RoleID = %q, want %q", updated.RoleID, role.ID)
	}
	// An unrelated field-only edit must not disturb the composition column
	// -- the reingest-preservation fix in upsertAgentDef.
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
	if afterUnrelated.RoleID != role.ID {
		t.Fatalf("an unrelated field edit wiped RoleID: got %q, want %q (upsertAgentDef reingest-preservation regression)", afterUnrelated.RoleID, role.ID)
	}

	// Clear role_id via PUT with an explicit empty string.
	clearBody, _ := json.Marshal(map[string]any{"role_id": ""})
	req = httptest.NewRequest("PUT", "/api/agents/"+created.ID, bytes.NewReader(clearBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /api/agents/{id} (clear role_id): expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var cleared store.AgentProfile
	if err := json.NewDecoder(w.Body).Decode(&cleared); err != nil {
		t.Fatalf("decode cleared agent: %v", err)
	}
	if cleared.RoleID != "" {
		t.Fatalf("after clear: RoleID = %q, want empty", cleared.RoleID)
	}
}
