package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestRolesCRUD_EndToEnd is the integration test required by
// TASKS/phase-1/01-add-roles-table-and-cascade-resolution.md's Done-means
// ("roles CRUD (store + REST) exists and is exercised by at least one
// integration test") -- exercises the full HTTP surface (create, list,
// get, update, delete) against a real migrated store, mirroring
// TestCreateAndListSessions's pattern.
func TestRolesCRUD_EndToEnd(t *testing.T) {
	_, mux := newTestAPI(t)

	// POST /api/roles
	createBody, _ := json.Marshal(CreateRoleRequest{
		Slug:            "sme",
		Name:            "Subject Matter Expert",
		SystemPrompt:    "You are a deeply knowledgeable SME.",
		DefaultClass:    "advisor",
		DefaultModel:    "claude-3-7-sonnet",
		DefaultProvider: "anthropic",
		DefaultTools:    `["dev_read","dev_grep"]`,
	})
	req := httptest.NewRequest("POST", "/api/roles", bytes.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /api/roles: expected 201, got %d; body: %s", w.Code, w.Body.String())
	}

	var created store.Role
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created role: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected role ID to be set")
	}
	if created.Slug != "sme" || created.SystemPrompt != "You are a deeply knowledgeable SME." {
		t.Errorf("created role mismatch: %+v", created)
	}

	// Rejects missing name/slug.
	badBody, _ := json.Marshal(CreateRoleRequest{Name: "No Slug"})
	req = httptest.NewRequest("POST", "/api/roles", bytes.NewReader(badBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/roles (missing slug): expected 400, got %d", w.Code)
	}

	// GET /api/roles
	req = httptest.NewRequest("GET", "/api/roles", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/roles: expected 200, got %d", w.Code)
	}
	var list []store.Role
	if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
		t.Fatalf("decode role list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 role, got %d", len(list))
	}

	// GET /api/roles/{id}
	req = httptest.NewRequest("GET", "/api/roles/"+created.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/roles/{id}: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// GET /api/roles/{id} for a nonexistent role -> 404.
	req = httptest.NewRequest("GET", "/api/roles/does-not-exist", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET /api/roles/{missing}: expected 404, got %d", w.Code)
	}

	// PUT /api/roles/{id}
	newName := "Renamed SME"
	newClass := "process"
	updateBody, _ := json.Marshal(UpdateRoleRequest{Name: &newName, DefaultClass: &newClass})
	req = httptest.NewRequest("PUT", "/api/roles/"+created.ID, bytes.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /api/roles/{id}: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var updated store.Role
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated role: %v", err)
	}
	if updated.Name != newName || updated.DefaultClass != newClass {
		t.Errorf("update did not apply: %+v", updated)
	}
	// SystemPrompt was left untouched by the partial update.
	if updated.SystemPrompt != created.SystemPrompt {
		t.Errorf("partial update should not have touched SystemPrompt: got %q, want %q", updated.SystemPrompt, created.SystemPrompt)
	}

	// DELETE /api/roles/{id}
	req = httptest.NewRequest("DELETE", "/api/roles/"+created.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE /api/roles/{id}: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/roles/"+created.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET /api/roles/{id} after delete: expected 404, got %d", w.Code)
	}
}

// TestCreateRole_InvalidClassRejected verifies the REST layer surfaces
// store.CreateRole's default_class validation error as a 500 (the same
// pattern skills.go/mcp_servers.go use for store-level validation errors --
// no separate REST-layer enum check is duplicated here).
func TestCreateRole_InvalidClassRejected(t *testing.T) {
	_, mux := newTestAPI(t)

	body, _ := json.Marshal(CreateRoleRequest{
		Slug:         "bad-class",
		Name:         "Bad Class",
		DefaultClass: "not-a-real-class",
	})
	req := httptest.NewRequest("POST", "/api/roles", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for invalid default_class, got %d; body: %s", w.Code, w.Body.String())
	}
}
