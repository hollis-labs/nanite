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

// TestConsumersCRUD_EndToEnd is the integration test required by
// TASKS/phase-5/01-build-assignment-api.md's Done-means ("consumers has
// full REST CRUD, exercised by at least one integration test") -- exercises
// the full HTTP surface (create, list, get, update, delete) against a real
// migrated store, mirroring TestRolesCRUD_EndToEnd's pattern exactly.
func TestConsumersCRUD_EndToEnd(t *testing.T) {
	_, mux := newTestAPI(t)

	// POST /api/consumers
	createBody, _ := json.Marshal(CreateConsumerRequest{
		Slug: "torque",
		Name: "Torque",
	})
	req := httptest.NewRequest("POST", "/api/consumers", bytes.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /api/consumers: expected 201, got %d; body: %s", w.Code, w.Body.String())
	}

	var created store.Consumer
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created consumer: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected consumer ID to be set")
	}
	if created.Slug != "torque" || created.Name != "Torque" {
		t.Errorf("created consumer mismatch: %+v", created)
	}

	// Rejects missing name/slug.
	badBody, _ := json.Marshal(CreateConsumerRequest{Name: "No Slug"})
	req = httptest.NewRequest("POST", "/api/consumers", bytes.NewReader(badBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/consumers (missing slug): expected 400, got %d", w.Code)
	}

	// GET /api/consumers -- migration 112 seeds 'loom' at boot, so this
	// consumer plus the new one makes 2.
	req = httptest.NewRequest("GET", "/api/consumers", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/consumers: expected 200, got %d", w.Code)
	}
	var list []store.Consumer
	if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
		t.Fatalf("decode consumer list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 consumers (seeded 'loom' + created 'torque'), got %d", len(list))
	}

	// GET /api/consumers/{id}
	req = httptest.NewRequest("GET", "/api/consumers/"+created.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/consumers/{id}: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// GET /api/consumers/{id} for a nonexistent consumer -> 404.
	req = httptest.NewRequest("GET", "/api/consumers/does-not-exist", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET /api/consumers/{missing}: expected 404, got %d", w.Code)
	}

	// PUT /api/consumers/{id}
	newName := "Torque Renamed"
	updateBody, _ := json.Marshal(UpdateConsumerRequest{Name: &newName})
	req = httptest.NewRequest("PUT", "/api/consumers/"+created.ID, bytes.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /api/consumers/{id}: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var updated store.Consumer
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated consumer: %v", err)
	}
	if updated.Name != newName {
		t.Errorf("update did not apply: %+v", updated)
	}
	// Slug was left untouched by the partial update.
	if updated.Slug != created.Slug {
		t.Errorf("partial update should not have touched Slug: got %q, want %q", updated.Slug, created.Slug)
	}

	// DELETE /api/consumers/{id}
	req = httptest.NewRequest("DELETE", "/api/consumers/"+created.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE /api/consumers/{id}: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/consumers/"+created.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET /api/consumers/{id} after delete: expected 404, got %d", w.Code)
	}
}

// TestHandleDeleteConsumer_RejectsWhileReferenced pins store.DeleteConsumer's
// documented FK-constraint behavior at the REST layer: a consumer still
// referenced by an agent_profiles.consumer_id row cannot be deleted.
func TestHandleDeleteConsumer_RejectsWhileReferenced(t *testing.T) {
	a, mux := newTestAPI(t)

	createBody, _ := json.Marshal(CreateConsumerRequest{Slug: "referenced", Name: "Referenced"})
	req := httptest.NewRequest("POST", "/api/consumers", bytes.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /api/consumers: expected 201, got %d; body: %s", w.Code, w.Body.String())
	}
	var consumer store.Consumer
	if err := json.NewDecoder(w.Body).Decode(&consumer); err != nil {
		t.Fatalf("decode consumer: %v", err)
	}

	agentBody, _ := json.Marshal(map[string]any{
		"name":          "Referencing Agent",
		"slug":          "referencing-agent",
		"system_prompt": "test",
		"consumer_id":   consumer.ID,
	})
	req = httptest.NewRequest("POST", "/api/agents", bytes.NewReader(agentBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /api/agents: expected 201, got %d; body: %s", w.Code, w.Body.String())
	}
	var agentResp store.AgentProfile
	if err := json.NewDecoder(w.Body).Decode(&agentResp); err != nil {
		t.Fatalf("decode agent: %v", err)
	}
	if agentResp.ConsumerID != consumer.ID {
		t.Fatalf("setup: agent.ConsumerID = %q, want %q", agentResp.ConsumerID, consumer.ID)
	}

	req = httptest.NewRequest("DELETE", "/api/consumers/"+consumer.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code == http.StatusOK {
		t.Fatalf("DELETE /api/consumers/{id} while referenced: expected a non-200 FK-constraint failure, got 200")
	}

	// Sanity: verify against the store directly too.
	if _, err := a.Services.Store.GetConsumer(context.Background(), consumer.ID); err != nil {
		t.Fatalf("GetConsumer after failed delete: %v", err)
	}
}
