package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

// Historical fixtures bypass retired writers only inside this private database.
// They never become runtime profiles or verified actors.
func retiredAgentHistory(t *testing.T, a *testAPI, source string) *store.AgentProfile {
	t.Helper()
	p := &store.AgentProfile{Name: "Retained", Slug: "retained-" + source, Source: source, SystemPrompt: "private historical prompt"}
	if err := storetest.HistoricalProfile(t.Context(), a.store, p); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.DB.ExecContext(t.Context(), `INSERT INTO agent_known_tools(agent_id,tool_name,reason) VALUES(?,?,?)`, p.ID, "historical-tool", "retained child"); err != nil {
		t.Fatal(err)
	}
	return p
}

// Compare complete persisted rows, including historical children and fresh
// definitions/settings/bindings/grants. A refusal must not convert or mutate
// either graph, even when its response looks correct.
func retiredAgentState(t *testing.T, a *testAPI) map[string][][]any {
	t.Helper()
	tables, err := a.store.DB.QueryContext(t.Context(), `SELECT name FROM sqlite_master WHERE type='table' AND (name LIKE 'agent_%' OR name LIKE 'actor_%' OR name='session_actor_bindings') ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var name string
		if scanErr := tables.Scan(&name); scanErr != nil {
			t.Fatal(scanErr)
		}
		names = append(names, name)
	}
	if rowsErr := tables.Err(); rowsErr != nil {
		t.Fatal(rowsErr)
	}
	if closeErr := tables.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	state := make(map[string][][]any, len(names))
	for _, name := range names {
		rows, queryErr := a.store.DB.QueryContext(t.Context(), fmt.Sprintf(`SELECT * FROM %q ORDER BY rowid`, name))
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		columns, columnErr := rows.Columns()
		if columnErr != nil {
			t.Fatal(columnErr)
		}
		state[name] = make([][]any, 0)
		for rows.Next() {
			values := make([]any, len(columns))
			targets := make([]any, len(columns))
			for i := range values {
				targets[i] = &values[i]
			}
			if scanErr := rows.Scan(targets...); scanErr != nil {
				t.Fatal(scanErr)
			}
			for i, value := range values {
				if raw, ok := value.([]byte); ok {
					values[i] = string(raw)
				}
			}
			state[name] = append(state[name], values)
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			t.Fatal(rowsErr)
		}
		if closeErr := rows.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	for _, required := range []string{"agent_profiles", "agent_definitions", "agent_host_settings", "agent_actor_bindings"} {
		if _, ok := state[required]; !ok {
			t.Fatalf("missing graph fixture table %s", required)
		}
	}
	return state
}

func assertRetiredAgentState(t *testing.T, a *testAPI, before map[string][][]any) {
	t.Helper()
	after := retiredAgentState(t, a)
	for table, rows := range before {
		if !reflect.DeepEqual(rows, after[table]) {
			t.Fatalf("refused request changed %s rows", table)
		}
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("refused request changed graph tables")
	}
}

func assertRetiredAgentResponse(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusGone {
		t.Fatalf("retired route = %d: %s", w.Code, w.Body.String())
	}
	var wire map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	message, ok := wire["error"].(string)
	if !ok || len(wire) != 1 || !strings.Contains(message, "retired") || !strings.Contains(message, "pinned definition") || strings.Contains(message, "private historical prompt") || strings.Contains(message, "write_secret") {
		t.Fatalf("unsafe or missing retirement guidance: %s", w.Body.String())
	}
}

func retiredAgentRequest(t *testing.T, h http.Handler, method, path, body string) {
	t.Helper()
	w, _ := mgReq(t, h, method, path, body)
	assertRetiredAgentResponse(t, w)
}

func TestRetiredAgentWritesPreserveHistoricalAndFreshGraphs(t *testing.T) {
	a, mux := newTestAPI(t)
	p := retiredAgentHistory(t, a, "user")
	before := retiredAgentState(t, a)
	for _, request := range []struct{ method, path, body string }{
		{"POST", "/api/agents", `{"name":"New","slug":"new-agent","role_tools":"[\"new-tool\"]","role_skills":"[\"new-skill\"]"}`},
		{"PUT", "/api/agents/" + p.ID, `{"name":"Changed","system_prompt":"changed","role_id":"claimed-role"}`},
		{"DELETE", "/api/agents/" + p.ID, ""},
		{"POST", "/api/agents/" + p.ID + "/revisions/claimed-revision/restore", `{}`},
	} {
		t.Run(request.method+request.path, func(t *testing.T) {
			retiredAgentRequest(t, mux, request.method, request.path, request.body)
			assertRetiredAgentState(t, a, before)
		})
	}
}

func TestRetiredAgentInputsNeverReachProfileWrites(t *testing.T) {
	a, mux := newTestAPI(t)
	if _, err := a.store.DB.ExecContext(t.Context(), `CREATE TRIGGER forbid_retired_insert BEFORE INSERT ON agent_profiles BEGIN SELECT RAISE(ABORT,'write_secret retired writer reached'); END`); err != nil {
		t.Fatal(err)
	}
	before := retiredAgentState(t, a)
	for _, body := range []string{`{`, `{"slug":"user"}`, `{"slug":"../escape","source":"internal"}`, `{"protocol":"invalid","transport":"invalid","role_id":"missing","consumer_id":"missing","model_id":"missing"}`} {
		retiredAgentRequest(t, mux, "POST", "/api/agents", body)
		assertRetiredAgentState(t, a, before)
	}
}

func TestRetiredAgentConcurrentCreatesHaveNoEffects(t *testing.T) {
	a, mux := newTestAPI(t)
	before := retiredAgentState(t, a)
	start := make(chan struct{})
	results := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/agents", strings.NewReader(`{"name":"Concurrent","slug":"same-agent"}`)))
			results[i] = w
		}()
	}
	close(start)
	wg.Wait()
	for _, w := range results {
		assertRetiredAgentResponse(t, w)
	}
	assertRetiredAgentState(t, a, before)
}

func TestRetiredAgentMuxKeepsRoutingControls(t *testing.T) {
	a, mux := newTestAPI(t)
	before := retiredAgentState(t, a)
	for _, request := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/api/health", http.StatusOK},
		{"GET", "/api/agent-definitions", http.StatusOK},
		{"PATCH", "/api/agents/nonexistent", http.StatusMethodNotAllowed},
		{"POST", "/api/agents/nonexistent/unregistered", http.StatusNotFound},
	} {
		w, _ := mgReq(t, mux, request.method, request.path, "")
		if w.Code != request.want {
			t.Fatalf("%s %s = %d, want %d", request.method, request.path, w.Code, request.want)
		}
	}
	w, _ := mgReq(t, mux, "POST", "/api/agent-host-settings", `{`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("active host settings input control = %d: %s", w.Code, w.Body.String())
	}
	assertRetiredAgentState(t, a, before)
}
