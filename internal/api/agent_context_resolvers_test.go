package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const retiredMutableAPIMessage = "Mutable profile operations are retired; author a pinned definition and configure host settings"

func retiredAPIRequest(t *testing.T, mux http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(w, r)
	return w
}

func requireRetiredAPI(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusGone {
		t.Fatalf("retired public operation returned %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 1 || body["error"] != retiredMutableAPIMessage {
		t.Fatalf("retired operation body=%v", body)
	}
}

// These private raw rows are retained history, never enrolled fresh actors.
func retiredAPIHistoricalProfile(t *testing.T, a *testAPI, id, source string) {
	t.Helper()
	if _, err := a.store.DB.ExecContext(context.Background(), `INSERT INTO agent_profiles(id,name,slug,system_prompt,source) VALUES(?,?,?,?,?)`, id, "Historical fixture", id, "Retained private historical prompt", source); err != nil {
		t.Fatal(err)
	}
}

func retiredAPISnapshot(t *testing.T, a *testAPI, query string) [][]any {
	t.Helper()
	rows, err := a.store.DB.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var snapshot [][]any
	for rows.Next() {
		cells := make([]any, len(columns))
		targets := make([]any, len(columns))
		for i := range cells {
			targets[i] = &cells[i]
		}
		if err = rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		for i, cell := range cells {
			if b, ok := cell.([]byte); ok {
				cells[i] = append([]byte(nil), b...)
			}
		}
		snapshot = append(snapshot, cells)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func retiredAPIHistoryUnchanged(t *testing.T, a *testAPI, query string, before [][]any) {
	t.Helper()
	if after := retiredAPISnapshot(t, a, query); !reflect.DeepEqual(before, after) {
		t.Fatalf("refused public operation changed %s: before=%v after=%v", query, before, after)
	}
}

func TestRetiredContextResolverPublicMuxRefusesAllOperationsAndPreservesHistory(t *testing.T) {
	a, mux := newTestAPI(t)
	const owner = "historical-resolver-owner"
	retiredAPIHistoricalProfile(t, a, owner, "user")
	retiredAPIHistoricalProfile(t, a, "historical-other-owner", "internal")
	if _, err := a.store.DB.ExecContext(t.Context(), `INSERT INTO agent_context_resolvers(id,agent_id,slot_name,kind,run,enabled,created_at,updated_at) VALUES('retained-resolver',?,'command','cmd','echo historical-private',1,'retained-created','retained-updated')`, owner); err != nil {
		t.Fatal(err)
	}
	const query = `SELECT * FROM agent_context_resolvers ORDER BY id`
	before := retiredAPISnapshot(t, a, query)
	for _, agentID := range []string{owner, "historical-other-owner", "missing-agent"} {
		base := "/api/agents/" + agentID + "/context-resolvers"
		for _, c := range []struct{ method, path, body string }{
			{"GET", base, ""}, {"GET", base + "/retained-resolver", ""}, {"GET", base + "/missing", ""},
			{"POST", base, `{"slot_name":"new","kind":"cmd","run":"echo new"}`},
			{"POST", base, "not json"}, {"POST", base, `{"kind":"role_summary"}`},
			{"PATCH", base + "/retained-resolver", `{"run":"changed","enabled":false}`},
			{"PATCH", base + "/retained-resolver", "not json"}, {"DELETE", base + "/retained-resolver", ""},
		} {
			t.Run(agentID+"/"+c.method+c.path, func(t *testing.T) {
				requireRetiredAPI(t, retiredAPIRequest(t, mux, c.method, c.path, c.body))
				retiredAPIHistoryUnchanged(t, a, query, before)
			})
		}
	}
	// The mux still distinguishes unsupported methods and unregistered paths.
	if w := retiredAPIRequest(t, mux, "PUT", "/api/agents/"+owner+"/context-resolvers/retained-resolver", `{}`); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unregistered resolver method: %d %s", w.Code, w.Body.String())
	}
	if w := retiredAPIRequest(t, mux, "GET", "/api/unregistered-context-resolvers", ""); w.Code != http.StatusNotFound {
		t.Fatalf("unknown route: %d %s", w.Code, w.Body.String())
	}
}
