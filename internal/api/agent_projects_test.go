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

// TestHandleAddAgentProject_RejectsNonexistentAgent is a regression pin for
// Phase 1 #05's precondition-verification finding: handleAddAgentProject
// previously performed no agent-existence check at all -- any agent_id
// string in the URL path landed directly in agent_projects. It must now be
// rejected with 404 when the agent has no backing agent_profiles row.
func TestHandleAddAgentProject_RejectsNonexistentAgent(t *testing.T) {
	a, mux := newTestAPI(t)

	proj := &store.Project{ID: "proj-agent-projects-test", Name: "Test Project"}
	if err := a.Services.Store.CreateProject(context.Background(), proj); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"project_id": proj.ID})
	req := httptest.NewRequest("POST", "/api/agents/does-not-exist/projects", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("POST /api/agents/{id}/projects for nonexistent agent: expected 404, got %d; body: %s", w.Code, w.Body.String())
	}

	projects, err := a.Services.Store.ListAgentProjects(context.Background(), "does-not-exist")
	if err != nil {
		t.Fatalf("ListAgentProjects: %v", err)
	}
	if len(projects) != 0 {
		t.Errorf("expected no agent_projects row for a rejected assignment, got %d", len(projects))
	}
}
