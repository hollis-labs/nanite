package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

var roleViewKeys = []string{
	"id", "slug", "name", "system_prompt", "default_class", "default_model",
	"default_provider", "default_tools", "default_skills", "default_permissions",
	"created_at", "updated_at", "plugin_id",
}

var projectViewKeys = []string{
	"id", "name", "description", "repo_path", "settings", "sort_order", "created_at", "updated_at",
}

func TestRoleViewJSON(t *testing.T) {
	var r store.Role
	populate(t, &r)
	assertKeys(t, "RoleView", mustJSON(t, roleToView(&r)), roleViewKeys)
	assertSameJSON(t, "populated", roleToView(&r), r)
	assertSameJSON(t, "zero", roleToView(&store.Role{}), store.Role{})
	assertSameJSON(t, "list", rolesToView([]store.Role{r, {}}), []store.Role{r, {}})
	assertSameJSON(t, "empty list", rolesToView([]store.Role{}), []store.Role{})
	assertSameJSON(t, "nil list", rolesToView(nil), []store.Role(nil))
}

func TestProjectViewJSON(t *testing.T) {
	var p store.Project
	populate(t, &p)
	assertKeys(t, "ProjectView", mustJSON(t, projectToView(&p)), projectViewKeys)
	assertSameJSON(t, "populated", projectToView(&p), p)
	assertSameJSON(t, "zero", projectToView(&store.Project{}), store.Project{})
	assertSameJSON(t, "list", projectsToView([]store.Project{p, {}}), []store.Project{p, {}})
	assertSameJSON(t, "empty list", projectsToView([]store.Project{}), []store.Project{})
	assertSameJSON(t, "nil list", projectsToView(nil), []store.Project(nil))
}

func TestRoles_StatusAndErrorBodies(t *testing.T) {
	_, mux := newTestAPI(t)
	for _, c := range []struct {
		name, method, path, body string
		code                     int
		msg                      string
	}{
		{"create bad body", "POST", "/api/roles", `not json`, 400, ""},
		{"create missing slug", "POST", "/api/roles", `{"name":"n"}`, 400, "name and slug are required"},
		{"get missing", "GET", "/api/roles/nope", "", 404, "role not found"},
		{"update missing beats bad body", "PUT", "/api/roles/nope", `not json`, 404, "role not found"},
		{"delete missing is 500", "DELETE", "/api/roles/nope", "", 500, `role "nope" not found`},
	} {
		w := mcpDo(mux, c.method, c.path, c.body)
		if w.Code != c.code {
			t.Fatalf("%s: status %d, want %d: %s", c.name, w.Code, c.code, w.Body.String())
		}
		if c.msg != "" && errorBody(t, w) != c.msg {
			t.Fatalf("%s: body %s, want error %q", c.name, w.Body.String(), c.msg)
		}
	}

	w := mcpDo(mux, "POST", "/api/roles", `{"name":"R","slug":"r"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created RoleView
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("create body %s: %v", w.Body.String(), err)
	}
	if bad := mcpDo(mux, "PUT", "/api/roles/"+created.ID, `not json`); bad.Code != http.StatusBadRequest {
		t.Fatalf("update bad body: %d %s", bad.Code, bad.Body.String())
	}
	w = mcpDo(mux, "PUT", "/api/roles/"+created.ID, `{"name":"R2"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"R2"`) || !strings.Contains(w.Body.String(), `"slug":"r"`) {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
}

func TestProjects_StatusAndErrorBodies(t *testing.T) {
	_, mux := newTestAPI(t)
	for _, c := range []struct {
		name, method, path, body string
		code                     int
		msg                      string
	}{
		{"create bad body", "POST", "/api/projects", `not json`, 400, ""},
		{"create missing id", "POST", "/api/projects", `{"name":"n","repo_path":"/"}`, 400, "id and name are required"},
		{"create bad repo", "POST", "/api/projects", `{"id":"p","name":"n","repo_path":"/"}`, 400, ""},
		{"update missing beats bad body", "PUT", "/api/projects/nope", `not json`, 404, "project not found"},
		{"delete missing", "DELETE", "/api/projects/nope", "", 404, "project not found"},
		{"create", "POST", "/api/projects", `{"id":"p","name":"n"}`, 201, ""},
		{"update bad body", "PUT", "/api/projects/p", `not json`, 400, ""},
		{"update bad repo", "PUT", "/api/projects/p", `{"name":"changed","repo_path":"/"}`, 400, ""},
	} {
		w := mcpDo(mux, c.method, c.path, c.body)
		if w.Code != c.code {
			t.Fatalf("%s: status %d, want %d: %s", c.name, w.Code, c.code, w.Body.String())
		}
		if c.msg != "" && errorBody(t, w) != c.msg {
			t.Fatalf("%s: body %s, want error %q", c.name, w.Body.String(), c.msg)
		}
		if strings.Contains(c.name, "bad repo") && !strings.HasPrefix(errorBody(t, w), "invalid repo_path: ") {
			t.Fatalf("%s: body %s, want an invalid repo_path error", c.name, w.Body.String())
		}
	}

	// A rejected repo_path writes nothing, even the fields sent beside it.
	w := mcpDo(mux, "GET", "/api/projects", "")
	if strings.Contains(w.Body.String(), "changed") {
		t.Fatalf("rejected update was partly written: %s", w.Body.String())
	}
	w = mcpDo(mux, "PUT", "/api/projects/p", `{"sort_order":3}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"sort_order":3`) || !strings.Contains(w.Body.String(), `"name":"n"`) {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	if w := mcpDo(mux, "DELETE", "/api/projects/p", ""); w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"deleted":"p"}` {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
}

// File autocomplete walks the session's project repository.
func TestAutocompleteFiles_WalksSessionProjectRepo(t *testing.T) {
	a, mux := newTestAPI(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	repo, err := os.MkdirTemp(home, "autocomplete-repo-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "zz-needle-file.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	body, _ := json.Marshal(map[string]string{"id": "ac", "name": "ac", "repo_path": repo})
	if w := mcpDo(mux, "POST", "/api/projects", string(body)); w.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", w.Code, w.Body.String())
	}
	sess := &store.Session{Provider: "anthropic", Model: "m", Status: "active", ProjectID: "ac"}
	if err := a.Services.Store.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	w := mcpDo(mux, "GET", "/api/autocomplete/files?q=zz-needle&session_id="+sess.ID, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"zz-needle-file.txt"`) {
		t.Fatalf("autocomplete: %d %s, want the file from the project repo", w.Code, w.Body.String())
	}
}
