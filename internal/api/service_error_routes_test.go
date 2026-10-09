package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func serviceErrorRoutes(t *testing.T) (*store.Store, http.Handler) {
	t.Helper()
	st, err := storetest.New(t, context.Background(), filepath.Join(t.TempDir(), "errors.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close(context.Background()) })
	svc := &service.Container{Agents: service.NewAgentService(service.AgentServiceConfig{Agents: st, Writers: st}), AgentConfig: service.NewAgentConfigService(st, agent.Classification{}, nil), AgentMembership: service.NewAgentMembershipService(st, st), Todos: service.NewTodoService(service.TodoServiceConfig{Todos: st, Plans: st}), Schedules: service.NewScheduleService(st), Streams: service.NewStreamManager()}
	mux := http.NewServeMux()
	New(svc).RegisterRoutes(mux)
	return st, mux
}
func serviceErrorRequest(t *testing.T, h http.Handler, method, path, body string, want int) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("%s %s = %d %s, want %d", method, path, rec.Code, rec.Body.String(), want)
	}
	var wire struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Error == "" || strings.Contains(wire.Error, "internal:") || strings.Contains(wire.Error, "not_found:") || strings.Contains(wire.Error, "conflict:") || strings.Contains(wire.Error, "permission:") || strings.Contains(wire.Error, "write_secret") || strings.Contains(wire.Error, "sql:") {
		t.Fatalf("unsafe flat error: %s", rec.Body.String())
	}
	return wire.Error
}
func TestServiceErrorRoutesClassifyExpectedFailures(t *testing.T) {
	st, h := serviceErrorRoutes(t)
	row := &store.AgentProfile{ID: "managed", Name: "Managed", Slug: "managed", SystemPrompt: "fixture", Source: "user"}
	if err := st.CreateAgent(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	serviceErrorRequest(t, h, "POST", "/api/agents", `{"name":"Duplicate","slug":"managed","system_prompt":"fixture"}`, 409)
	serviceErrorRequest(t, h, "POST", "/api/agents/managed/copy-to-managed", `{}`, 409)
	serviceErrorRequest(t, h, "POST", "/api/agents/missing/copy-to-managed", `{}`, 404)
	serviceErrorRequest(t, h, "GET", "/api/agents/missing/known-tools", "", 404)
	serviceErrorRequest(t, h, "PATCH", "/api/todos/missing/scope", `{"scope":"session","scope_id":"fixture"}`, 404)
	serviceErrorRequest(t, h, "PATCH", "/api/todos/missing/scope", `{"scope":"bad"}`, 400)
	serviceErrorRequest(t, h, "POST", "/api/schedules", `{"agent_id":"missing"}`, 404)
}
func TestServiceErrorRoutesAgentWriteCausesStayInLogs(t *testing.T) {
	for _, op := range []string{"create", "update", "copy"} {
		t.Run(op, func(t *testing.T) {
			st, h := serviceErrorRoutes(t)
			source := "user"
			if op == "copy" {
				source = "plugin"
			}
			if err := st.CreateAgent(context.Background(), &store.AgentProfile{ID: "target", Name: "Target", Slug: "target", SystemPrompt: "fixture", Source: source}); err != nil {
				t.Fatal(err)
			}
			statement := "INSERT"
			if op == "update" {
				statement = "UPDATE"
			}
			if _, err := st.DB.Exec(`CREATE TRIGGER fail_agent_write BEFORE ` + statement + ` ON agent_profiles BEGIN SELECT RAISE(ABORT, 'write_secret SQLITE_BUSY private query'); END`); err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			method, path, body := "POST", "/api/agents", `{"name":"Fresh","slug":"fresh","system_prompt":"fixture"}`
			if op == "update" {
				method, path, body = "PUT", "/api/agents/target", `{"name":"Changed"}`
			}
			if op == "copy" {
				path, body = "/api/agents/target/copy-to-managed", `{}`
			}
			serviceErrorRequest(t, h, method, path, body, 500)
			if !strings.Contains(logs.String(), "write_secret") {
				t.Fatalf("write cause absent from log: %s", logs.String())
			}
		})
	}
}
func TestServiceErrorRoutesClosedDBIsInternal(t *testing.T) {
	st, h := serviceErrorRoutes(t)
	if err := st.DB.Close(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/agents", `{"name":"Fresh","slug":"fresh","system_prompt":"fixture"}`},
		{"POST", "/api/durable-agents", `{"slug":"fixture","profile_id":"target"}`},
		{"DELETE", "/api/agents/target/projects/project", ""},
		{"DELETE", "/api/sessions/session/agents/target", ""},
		{"GET", "/api/agents/target", ""},
		{"GET", "/api/agents/target/known-tools", ""},
		{"DELETE", "/api/agents/target", ""},
		{"POST", "/api/sessions/fixture/agents", `{"agent_id":"target"}`},
		{"POST", "/api/agents/target/projects", `{"project_id":"fixture"}`},
		{"PUT", "/api/agents/target", `{"name":"Changed"}`},
		{"POST", "/api/agents/target/copy-to-managed", `{}`},
		{"PATCH", "/api/todos/missing/scope", `{"scope":"session","scope_id":"fixture"}`},
		{"POST", "/api/schedules", `{"agent_id":"target"}`},
	} {
		serviceErrorRequest(t, h, c.method, c.path, c.body, 500)
	}
}

func TestServiceErrorRoutesScheduleWriteFailure(t *testing.T) {
	st, h := serviceErrorRoutes(t)
	if err := st.CreateAgent(context.Background(), &store.AgentProfile{ID: "schedule-agent", Slug: "schedule-agent", Name: "Schedule", SystemPrompt: "fixture", Source: "user"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`CREATE TRIGGER fail_schedule_write BEFORE INSERT ON agent_schedules BEGIN SELECT RAISE(ABORT, 'write_secret SQLITE_BUSY private schedule query'); END`); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	serviceErrorRequest(t, h, "POST", "/api/schedules", `{"agent_id":"schedule-agent","name":"Fixture","schedule_kind":"one_shot","schedule_spec":"2099-01-01T00:00:00Z","body":"fixture"}`, 500)
	if !strings.Contains(logs.String(), "write_secret") {
		t.Fatalf("schedule cause absent from log: %s", logs.String())
	}
}

func TestServiceErrorRoutesAssignmentsKeepValidationSeparateFromWriteFailure(t *testing.T) {
	st, h := serviceErrorRoutes(t)
	serviceErrorRequest(t, h, "POST", "/api/agents", `{"name":"Invalid Role","slug":"invalid-role","system_prompt":"fixture","role_id":"missing"}`, 400)
	serviceErrorRequest(t, h, "POST", "/api/agents", `{"name":"Invalid Protocol","slug":"invalid-protocol","system_prompt":"fixture","protocol":"made-up"}`, 400)
	if _, err := st.DB.Exec(`CREATE TRIGGER fail_assignment_write BEFORE INSERT ON agent_profiles BEGIN SELECT RAISE(ABORT, 'write_secret SQLITE_BUSY private assignment query'); END`); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	role := &store.Role{Slug: "assignment-role", Name: "Assignment Role", SystemPrompt: "fixture"}
	if err := st.CreateRole(t.Context(), role); err != nil {
		t.Fatal(err)
	}
	serviceErrorRequest(t, h, "POST", "/api/agents", fmt.Sprintf(`{"name":"Assignment","slug":"assignment","system_prompt":"fixture","role_id":%q}`, role.ID), 500)
	if !strings.Contains(logs.String(), "write_secret") {
		t.Fatalf("assignment cause absent from log: %s", logs.String())
	}
}

func TestServiceErrorRoutesDurableAgentMissingProfile(t *testing.T) {
	_, h := serviceErrorRoutes(t)
	message := serviceErrorRequest(t, h, "POST", "/api/durable-agents", `{"slug":"fixture","profile_id":"missing"}`, 404)
	if !strings.Contains(message, "create or import it through the agent API first") {
		t.Fatalf("missing profile guidance: %q", message)
	}
}
func TestServiceErrorRoutesAgentBehaviorValidation(t *testing.T) {
	st, h := serviceErrorRoutes(t)
	if err := st.CreateAgent(context.Background(), &store.AgentProfile{ID: "target", Slug: "target", Name: "Target", Source: "user"}); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"class", "activation_mode", "default_state"} {
		for _, method := range []string{"POST", "PUT"} {
			path := "/api/agents"
			if method == "PUT" {
				path += "/target"
			}
			body := fmt.Sprintf(`{"name":"Fixture","slug":"fresh","system_prompt":"fixture","%s":"bogus"}`, field)
			message := serviceErrorRequest(t, h, method, path, body, 400)
			if !strings.Contains(message, field) {
				t.Fatalf("missing field in safe message: %s", message)
			}
		}
	}
}

func TestServiceErrorRoutesMembershipRemoval(t *testing.T) {
	for _, kind := range []string{"project", "session"} {
		t.Run(kind, func(t *testing.T) {
			st, h := serviceErrorRoutes(t)
			ctx := context.Background()
			if err := st.CreateAgent(ctx, &store.AgentProfile{ID: "member", Name: "Member", Slug: "member", Source: "user"}); err != nil {
				t.Fatal(err)
			}
			path, table := "/api/agents/member/projects/project", "agent_projects"
			if kind == "project" {
				if err := st.CreateProject(ctx, &store.Project{ID: "project", Name: "Project"}); err != nil {
					t.Fatal(err)
				}
				if err := st.AddAgentProject(ctx, "member", "project"); err != nil {
					t.Fatal(err)
				}
			} else {
				path, table = "/api/sessions/session/agents/member", "session_agents"
				if err := st.CreateSession(ctx, &store.Session{ID: "session", Title: "Session"}); err != nil {
					t.Fatal(err)
				}
				if err := st.EnsureSessionAgent(ctx, "session", "member", "collaborate", false); err != nil {
					t.Fatal(err)
				}
			}
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			if _, err := st.DB.Exec("CREATE TRIGGER fail_membership_delete BEFORE DELETE ON " + table + " BEGIN SELECT RAISE(ABORT, 'write_secret private membership query'); END"); err != nil {
				t.Fatal(err)
			}
			serviceErrorRequest(t, h, "DELETE", path, "", 500)
			if !strings.Contains(logs.String(), "write_secret") || !strings.Contains(logs.String(), "route=") || !strings.Contains(logs.String(), "member") {
				t.Fatalf("missing private cause or route/id: %s", logs.String())
			}
			if _, err := st.DB.Exec("DROP TRIGGER fail_membership_delete"); err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("DELETE", path, nil))
			if rec.Code != 200 {
				t.Fatalf("first delete = %d %s", rec.Code, rec.Body.String())
			}
			serviceErrorRequest(t, h, "DELETE", path, "", 404)
		})
	}
}
