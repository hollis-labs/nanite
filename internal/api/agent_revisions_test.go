package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func agentRevisionRequest(t *testing.T, mux http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(w, r)
	return w
}

func TestAgentPUTStrictBodiesCannotErasePrompt(t *testing.T) {
	a, mux := newTestAPI(t)
	ctx := context.Background()
	p := &store.AgentProfile{Name: "Safe", Slug: "strict-put", SystemPrompt: "Never erase this prompt", Source: "user"}
	if err := a.store.CreateAgent(ctx, p); err != nil {
		t.Fatal(err)
	}
	get := agentRevisionRequest(t, mux, "GET", "/api/agents/"+p.ID, "")
	if get.Code != 200 {
		t.Fatal(get.Body.String())
	}
	cases := []string{"", "null", "[]", "{}", `{"revision":"only-token"}`, `{"agent":{}}`, get.Body.String(), `{"name":null}`, `{"role_id":null}`, `{"revision":null}`, `{"Name":"case alias"}`, `{"unknown":true}`, `{"name":"a","name":"b"}`, `{"name":""}`, `{"slug":" "}`, `{"system_prompt":" "}`, `{"can_execute":"false"}`, `{"name":"a"} {}`, `{"name":"a"} trailing`, `{"settings":"null"}`, `{"role_tools":"{}"}`, `{"status":"unsupported"}`, `{"protocol":"made-up"}`}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			w := agentRevisionRequest(t, mux, "PUT", "/api/agents/"+p.ID, body)
			if w.Code != 400 {
				t.Fatalf("body %s returned %d: %s", body, w.Code, w.Body.String())
			}
			current, err := a.store.GetAgent(ctx, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current.Revision != p.Revision || current.SystemPrompt != p.SystemPrompt || current.Name != p.Name {
				t.Fatalf("refused body mutated profile: %#v", current)
			}
		})
	}
}

func TestAgentPUTPartialFieldsPreserveAssignmentsAndDetectStaleRevision(t *testing.T) {
	a, mux := newTestAPI(t)
	ctx := context.Background()
	role := &store.Role{Slug: "partial-role", Name: "Partial Role"}
	if err := a.store.CreateRole(ctx, role); err != nil {
		t.Fatal(err)
	}
	p := &store.AgentProfile{Name: "Original", Slug: "partial-fields", SystemPrompt: "Original prompt", Source: "user", SourceRef: "/provenance/only.md", RoleID: role.ID, Protocol: "acp", Transport: "stdio", CanExecute: true, DefaultProvider: "original-provider"}
	if err := a.store.CreateAgent(ctx, p); err != nil {
		t.Fatal(err)
	}
	w := agentRevisionRequest(t, mux, "PUT", "/api/agents/"+p.ID, `{"description":"edited","can_execute":false}`)
	if w.Code != 200 {
		t.Fatalf("partial PUT = %d: %s", w.Code, w.Body.String())
	}
	var got AgentProfileView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Description != "edited" || got.CanExecute || got.SystemPrompt != p.SystemPrompt || got.Name != p.Name || got.RoleID != role.ID || got.Protocol != "acp" || got.Transport != "stdio" || got.SourceRef != p.SourceRef || got.DefaultProvider != p.DefaultProvider || got.Revision == p.Revision || got.Revision == "" {
		t.Fatalf("partial update lost fields: %#v", got)
	}
	body, _ := json.Marshal(map[string]string{"name": "stale edit", "revision": p.Revision})
	w = agentRevisionRequest(t, mux, "PUT", "/api/agents/"+p.ID, string(body))
	if w.Code != 409 {
		t.Fatalf("stale PUT = %d: %s", w.Code, w.Body.String())
	}
	w = agentRevisionRequest(t, mux, "PUT", "/api/agents/"+p.ID, `{"role_id":"","protocol":"","transport":"","default_provider":"updated-provider","runtime_kind":"api"}`)
	if w.Code != 200 {
		t.Fatalf("clear assignments = %d: %s", w.Code, w.Body.String())
	}
	current, err := a.store.GetAgent(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.RoleID != "" || current.Protocol != "" || current.Transport != "" || current.DefaultProvider != "updated-provider" || current.RuntimeKind != "api" || current.SystemPrompt != p.SystemPrompt {
		t.Fatalf("explicit clears not applied: %#v", current)
	}
}

func TestAgentRevisionAPIListsAndLabelsPartialRestore(t *testing.T) {
	a, mux := newTestAPI(t)
	ctx := context.Background()
	p := &store.AgentProfile{Name: "Original", Slug: "revision-api", SystemPrompt: "Original prompt", Source: "user"}
	if err := a.store.CreateAgent(ctx, p); err != nil {
		t.Fatal(err)
	}
	w := agentRevisionRequest(t, mux, "PUT", "/api/agents/"+p.ID, `{"system_prompt":"Changed prompt"}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var current AgentProfileView
	if err := json.Unmarshal(w.Body.Bytes(), &current); err != nil {
		t.Fatal(err)
	}
	w = agentRevisionRequest(t, mux, "GET", "/api/agents/"+p.ID+"/revisions?limit=1&offset=1", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var page struct {
		Revisions []store.AgentRevision `json:"revisions"`
		Scope     string                `json:"restore_scope"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Revisions) != 1 || page.Revisions[0].ID != p.Revision || page.Scope != "partial_profile_and_assignments" {
		t.Fatalf("history page = %#v", page)
	}
	path := "/api/agents/" + p.ID + "/revisions/" + p.Revision + "/restore"
	w = agentRevisionRequest(t, mux, "POST", path, `{}`)
	if w.Code != 400 {
		t.Fatalf("restore without current revision = %d", w.Code)
	}
	body, _ := json.Marshal(map[string]string{"revision": current.Revision})
	w = agentRevisionRequest(t, mux, "POST", path, string(body))
	if w.Code != 200 {
		t.Fatalf("restore = %d: %s", w.Code, w.Body.String())
	}
	var restored struct {
		Agent  AgentProfileView `json:"agent"`
		Scope  string           `json:"restore_scope"`
		Grants bool             `json:"grants_restored"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Agent.SystemPrompt != p.SystemPrompt || restored.Agent.Revision == current.Revision || restored.Scope != "partial_profile_and_assignments" || restored.Grants {
		t.Fatalf("restore response = %#v", restored)
	}
	w = agentRevisionRequest(t, mux, "POST", path, string(body))
	if w.Code != 409 {
		t.Fatalf("repeated stale restore = %d: %s", w.Code, w.Body.String())
	}
}
