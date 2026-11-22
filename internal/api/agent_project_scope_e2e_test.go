package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

// Project membership exercises a private prior-authorized actor, never a host UUID.
func TestAgentProjectScope_EndToEnd(t *testing.T) {
	a, mux := newTestAPI(t)

	agent := &store.AgentProfile{Name: "Scope Agent", Slug: "scope-agent", SystemPrompt: "x"}
	if err := storetest.PriorAuthorizedActor(t.Context(), a.store, agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// 1. Create a project via the flat /api/projects route.
	createBody, _ := json.Marshal(map[string]string{
		"id":   "proj-scope-e2e",
		"name": "Scope E2E Project",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/projects", bytes.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /api/projects = %d body=%s", w.Code, w.Body.String())
	}
	var project store.Project
	if err := json.NewDecoder(w.Body).Decode(&project); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	if project.ID != "proj-scope-e2e" {
		t.Fatalf("project.ID = %q, want proj-scope-e2e", project.ID)
	}

	// 2. Assign the project to the agent.
	assignBody, _ := json.Marshal(map[string]string{"project_id": project.ID})
	req = httptest.NewRequest(http.MethodPost, "/api/agents/"+url.PathEscape(agent.ID)+"/projects", bytes.NewReader(assignBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /api/agents/%s/projects = %d body=%s", agent.ID, w.Code, w.Body.String())
	}

	// 3. List from the agent side.
	req = httptest.NewRequest(http.MethodGet, "/api/agents/"+url.PathEscape(agent.ID)+"/projects", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/agents/%s/projects = %d body=%s", agent.ID, w.Code, w.Body.String())
	}
	var agentProjects []store.Project
	if err := json.NewDecoder(w.Body).Decode(&agentProjects); err != nil {
		t.Fatalf("decode agent projects: %v", err)
	}
	if len(agentProjects) != 1 || agentProjects[0].ID != project.ID {
		t.Fatalf("agent projects = %+v, want [%s]", agentProjects, project.ID)
	}

	// 4. Unassign.
	req = httptest.NewRequest(http.MethodDelete, "/api/agents/"+url.PathEscape(agent.ID)+"/projects/"+project.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE /api/agents/%s/projects/%s = %d body=%s", agent.ID, project.ID, w.Code, w.Body.String())
	}

	// 5. Confirm the unassignment took.
	req = httptest.NewRequest(http.MethodGet, "/api/agents/"+url.PathEscape(agent.ID)+"/projects", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/agents/%s/projects (after delete) = %d body=%s", agent.ID, w.Code, w.Body.String())
	}
	var agentProjectsAfter []store.Project
	if err := json.NewDecoder(w.Body).Decode(&agentProjectsAfter); err != nil {
		t.Fatalf("decode agent projects after delete: %v", err)
	}
	if len(agentProjectsAfter) != 0 {
		t.Fatalf("agent projects after delete = %+v, want empty", agentProjectsAfter)
	}
}

// TestProjectsAPI_FlatCRUD_EndToEnd proves the flat /api/projects surface
// (this task's step-2 disambiguation resolution) supports full CRUD, not
// just create — GET (list), PUT (update), DELETE — since no workspace_id
// path segment exists anymore to scope these calls by.
func TestProjectsAPI_FlatCRUD_EndToEnd(t *testing.T) {
	_, mux := newTestAPI(t)

	createBody, _ := json.Marshal(map[string]string{
		"id":   "proj-crud-e2e",
		"name": "CRUD E2E Project",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/projects", bytes.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /api/projects = %d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/projects = %d body=%s", w.Code, w.Body.String())
	}
	var projects []store.Project
	if err := json.NewDecoder(w.Body).Decode(&projects); err != nil {
		t.Fatalf("decode projects: %v", err)
	}
	found := false
	for _, p := range projects {
		if p.ID == "proj-crud-e2e" {
			found = true
		}
	}
	if !found {
		t.Fatalf("created project not present in GET /api/projects: %+v", projects)
	}

	updateBody, _ := json.Marshal(map[string]string{"name": "Renamed"})
	req = httptest.NewRequest(http.MethodPut, "/api/projects/proj-crud-e2e", bytes.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /api/projects/proj-crud-e2e = %d body=%s", w.Code, w.Body.String())
	}
	var updated store.Project
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated project: %v", err)
	}
	if updated.Name != "Renamed" {
		t.Fatalf("updated.Name = %q, want Renamed", updated.Name)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/projects/proj-crud-e2e", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE /api/projects/proj-crud-e2e = %d body=%s", w.Code, w.Body.String())
	}
}
