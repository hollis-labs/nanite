package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestAgentV1CreateSession_ProjectMustResolveRepoPath pins
// CW-20261001-0020's create-time guard: a project-scoped session's agent
// works in the project's repo_path, so an unknown project, or one whose
// repo_path is set but does not resolve, is refused up front instead of
// booting an agent that cannot see its project. A project with no
// repo_path is allowed, matching the boot path, which warns and boots
// without a work root: plain API-chat sessions live in such projects.
func TestAgentV1CreateSession_ProjectMustResolveRepoPath(t *testing.T) {
	a, mux := newTestAPI(t)
	allowTestNativeModel(a)
	ctx := context.Background()
	for _, p := range []*store.Project{
		{ID: "with-repo", Name: "with-repo", RepoPath: t.TempDir()},
		{ID: "no-repo", Name: "no-repo"},
		{ID: "gone-repo", Name: "gone-repo", RepoPath: filepath.Join(t.TempDir(), "missing")},
	} {
		if err := a.store.CreateProject(ctx, p); err != nil {
			t.Fatalf("CreateProject %s: %v", p.ID, err)
		}
	}

	create := func(req agentV1CreateSessionRequest) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(req)
		r := httptest.NewRequest(http.MethodPost, "/api/agent/v1/sessions", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}

	for _, tc := range []struct {
		name string
		req  agentV1CreateSessionRequest
		want int
	}{
		{"project with an existing repo_path", agentV1CreateSessionRequest{ProjectID: "with-repo", DefinitionRef: a.Services.CognitiveViews.DefaultDefinitionRef}, http.StatusCreated},
		{"no project", agentV1CreateSessionRequest{DefinitionRef: a.Services.CognitiveViews.DefaultDefinitionRef}, http.StatusCreated},
		{"unknown project", agentV1CreateSessionRequest{ProjectID: "nope", DefinitionRef: a.Services.CognitiveViews.DefaultDefinitionRef}, http.StatusNotFound},
		{"project with no repo_path", agentV1CreateSessionRequest{ProjectID: "no-repo", DefinitionRef: a.Services.CognitiveViews.DefaultDefinitionRef}, http.StatusCreated},
		{"project whose repo_path is gone", agentV1CreateSessionRequest{ProjectID: "gone-repo", DefinitionRef: a.Services.CognitiveViews.DefaultDefinitionRef}, http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if w := create(tc.req); w.Code != tc.want {
				t.Fatalf("create = %d, want %d; body=%s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}
