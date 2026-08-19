package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestAgentProjectScope_EndToEnd is the "Done means" manual verification
// for Phase 0 item 20
// (TASKS/phase-0/20-retire-workspaces-and-instance-mechanism.md): a real,
// permanent regression test (no prior test in this package covered the
// route at all) proving agent_projects — the live Agent Construction
// scope mechanism (docs/engineering/architecture/01-agent-construction.md)
// — still works end-to-end over the real HTTP mux after this task's
// disambiguation finding: `projects` is kept (only its workspace_id
// column/FK is dropped), and `agent_projects` + its REST routes are
// completely untouched.
//
// Exercises, in order, against the real mux (no store-layer shortcuts):
//  1. POST /api/projects — the flat, workspace-less create route this
//     task's step 2 resolved to (verified against the real frontend
//     callers: ScopeSelector.tsx, NewProjectDialog.tsx,
//     WorkspaceProjectManager.tsx/ProjectManager.tsx, CreateProjectModal.tsx).
//  2. POST /api/agents/{id}/projects — assign.
//  3. GET /api/agents/{id}/projects — list from the agent side.
//  4. DELETE /api/agents/{id}/projects/{projectId} — unassign.
//  5. GET /api/agents/{id}/projects again — confirm the unassignment took.
//
// NOTE: the reverse route, GET /api/projects/{id}/agents (handleListProjectAgents
// -> store.ListProjectAgents), is NOT exercised here. It has a pre-existing,
// unrelated bug — "ambiguous column name: created_at" — because
// store.agentColumns (internal/store/agents.go) selects bare `created_at`/
// `updated_at` and ListProjectAgents's query JOINs agent_profiles with
// agent_projects, which also has its own created_at column (migration
// 001_schema.sql). This function and agentColumns are both untouched by
// this task's diff (confirmed via `git diff HEAD -- internal/store/agent_projects.go`,
// which only touches ListAgentProjects) and this task's brief requires
// `agent_projects` be kept fully intact, so the pre-existing bug is left
// as-is and reported separately rather than fixed here.
func TestAgentProjectScope_EndToEnd(t *testing.T) {
	a, mux := newTestAPI(t)

	agent := &store.AgentProfile{Name: "Scope Agent", Slug: "scope-agent", SystemPrompt: "x"}
	if err := a.Services.Store.CreateAgent(agent); err != nil {
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
	req = httptest.NewRequest(http.MethodPost, "/api/agents/"+agent.ID+"/projects", bytes.NewReader(assignBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /api/agents/%s/projects = %d body=%s", agent.ID, w.Code, w.Body.String())
	}

	// 3. List from the agent side.
	req = httptest.NewRequest(http.MethodGet, "/api/agents/"+agent.ID+"/projects", nil)
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
	req = httptest.NewRequest(http.MethodDelete, "/api/agents/"+agent.ID+"/projects/"+project.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE /api/agents/%s/projects/%s = %d body=%s", agent.ID, project.ID, w.Code, w.Body.String())
	}

	// 5. Confirm the unassignment took.
	req = httptest.NewRequest(http.MethodGet, "/api/agents/"+agent.ID+"/projects", nil)
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
