package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	svcerr "github.com/hollis-labs/go-svcerr"
	tp "github.com/hollis-labs/go-transportparity"
	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/api"
	"github.com/hollis-labs/nanite/internal/selftools"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// These doors are the registered REST routes and an MCP SDK client sending
// tools/call to Nanite's real server. Neither door is replaced by a service
// closure; categorization below reads only what crossed the wire.
func parityDoors(t *testing.T) (*store.Store, http.Handler, *sdk.ClientSession) {
	t.Helper()
	st, err := storetest.New(t, context.Background(), filepath.Join(t.TempDir(), "parity.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close(context.Background()) })
	services := &service.Container{
		Todos:       service.NewTodoService(service.TodoServiceConfig{Todos: st, Plans: st}),
		Streams:     service.NewStreamManager(),
		Agents:      service.NewAgentService(service.AgentServiceConfig{Agents: st, Writers: st}),
		AgentConfig: service.NewAgentConfigService(st, agent.Classification{}, nil),
	}
	mux := http.NewServeMux()
	api.New(services).RegisterRoutes(mux)
	server := New(st, "parity-session", nil, "", []string{"todo_update", "agent_update", "agent_create"})
	return st, mux, connectClient(t, server)
}

func httpOutcome(t *testing.T, h http.Handler, method, path string, args any) tp.Outcome {
	t.Helper()
	status, raw := tp.HTTPJSON(t, h, method, path, args)
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if status < 400 {
		return tp.FromValue(body)
	}
	// HTTP intentionally has a flat envelope, and the agent ownership refusal
	// has its own richer shape. Map the actual HTTP contract explicitly.
	code := svcerr.CodeInternal
	switch status {
	case http.StatusBadRequest:
		code = svcerr.CodeInvalid
	case http.StatusNotFound:
		code = svcerr.CodeNotFound
	case http.StatusForbidden:
		code = svcerr.CodePermission
	case http.StatusConflict:
		if body["error"] == "agent_not_managed" {
			code = svcerr.CodePermission
		} else {
			code = svcerr.CodeConflict
		}
	}
	return tp.Outcome{Category: code, Status: status, Detail: string(raw)}
}

func mcpOutcome(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) tp.Outcome {
	t.Helper()
	result, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("MCP dispatch: %v", err)
	}
	text := callText(result)
	var body map[string]any
	if err := json.Unmarshal([]byte(text), &body); err != nil {
		t.Fatalf("MCP JSON: %v: %s", err, text)
	}
	if !result.IsError {
		return tp.FromValue(body)
	}
	code, ok := body["code"].(string)
	if !ok || code == "" {
		t.Fatalf("uncategorized MCP failure: %s", text)
	}
	return tp.Outcome{Category: svcerr.Code(code), Detail: text}
}

func TestTransportParityTodoUpdate(t *testing.T) {
	st, h, cs := parityDoors(t)
	cases := []struct {
		name     string
		args     map[string]any
		missing  bool
		category svcerr.Code
	}{
		{name: "all request fields", args: map[string]any{"title": "changed", "description": "new description", "status": "done", "priority": "high", "labels": `["parity"]`, "metadata": `{"owner":"operator"}`}},
		{name: "explicit empty strings", args: map[string]any{"title": "", "description": "", "labels": "", "metadata": ""}},
		{name: "omitted fields", args: map[string]any{}},
		{name: "null leaves fields unchanged", args: map[string]any{"title": nil, "description": nil, "status": nil, "priority": nil, "labels": nil, "metadata": nil}},
		{name: "invalid status", args: map[string]any{"status": "bogus"}, category: svcerr.CodeInvalid},
		{name: "empty status", args: map[string]any{"status": ""}, category: svcerr.CodeInvalid},
		{name: "empty priority", args: map[string]any{"priority": ""}, category: svcerr.CodeInvalid},
		{name: "labels array rejected", args: map[string]any{"labels": []string{"wrong shape"}}, category: svcerr.CodeInvalid},
		{name: "invalid priority", args: map[string]any{"priority": "bogus"}, category: svcerr.CodeInvalid},
		{name: "missing todo", args: map[string]any{"title": "never stored"}, missing: true, category: svcerr.CodeNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := &store.Todo{Title: "original", Description: "keep", Scope: "session", ScopeID: "parity-session", Metadata: `{"keep":true}`}
			if err := st.CreateTodo(context.Background(), row); err != nil {
				t.Fatal(err)
			}
			id := row.ID
			if tc.missing {
				id = "missing-todo"
			}
			// Each door starts from the identical persisted row. Restore between
			// writes so omission and clearing are tested, not hidden by the first call.
			original := *row
			a := httpOutcome(t, h, http.MethodPut, "/api/todos/"+id, tc.args)
			if tc.category != "" {
				after, err := st.GetTodo(context.Background(), row.ID)
				if err != nil {
					t.Fatal(err)
				}
				if after.Title != original.Title || after.Status != original.Status || after.Priority != original.Priority || after.Metadata != original.Metadata {
					t.Fatalf("HTTP rejected operation changed todo: %+v", after)
				}
			}
			if err := st.UpdateTodo(context.Background(), &original); err != nil {
				t.Fatal(err)
			}
			args := map[string]any{"id": id}
			for key, value := range tc.args {
				args[key] = value
			}
			b := mcpOutcome(t, cs, "todo_update", args)
			if tc.category != "" {
				if a.OK || a.Category != tc.category || b.OK || b.Category != tc.category {
					t.Fatalf("want %s: HTTP %s, MCP %s", tc.category, a, b)
				}
				after, err := st.GetTodo(context.Background(), row.ID)
				if err != nil {
					t.Fatal(err)
				}
				if after.Title != original.Title || after.Status != original.Status || after.Priority != original.Priority {
					t.Fatalf("rejected operation changed todo: %+v", after)
				}
			} else {
				// UpdatedAt is server-generated metadata, not the operation's value.
				delete(a.Value.(map[string]any), "updated_at")
				delete(b.Value.(map[string]any), "updated_at")
			}
			tp.AssertSameOutcome(t, tc.name, a, b)
		})
	}
}

func TestTransportParityTodoUpdateFields(t *testing.T) {
	_, _, cs := parityDoors(t)
	tp.AssertSameFieldNames(t, "HTTP todo update", "MCP decoded todo update", tp.AcceptedFieldNames(service.TodoUpdates{}), tp.AcceptedFieldNames(selftools.TodoUpdateFields{}))
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "todo_update" {
			raw, err := json.Marshal(tool.InputSchema)
			if err != nil {
				t.Fatal(err)
			}
			var schema struct {
				Properties map[string]any `json:"properties"`
			}
			if err := json.Unmarshal(raw, &schema); err != nil {
				t.Fatal(err)
			}
			// id is the REST path parameter, not an update field.
			delete(schema.Properties, "id")
			var actual []string
			for key := range schema.Properties {
				actual = append(actual, key)
			}
			sort.Strings(actual)
			expected := tp.AcceptedFieldNames(service.TodoUpdates{})
			tp.AssertSameOutcome(t, "advertised MCP vs accepted HTTP fields", tp.FromValue(expected), tp.FromValue(actual))
			return
		}
	}
	t.Fatal("todo_update absent from real MCP discovery")
}

func TestTransportParityAgentUpdateRefusal(t *testing.T) {
	st, h, cs := parityDoors(t)
	for _, source := range []string{"internal", "plugin", "external"} {
		t.Run(source, func(t *testing.T) {
			row := &store.AgentProfile{Name: "Protected", Slug: "protected-" + source, SystemPrompt: "keep prompt", Source: source}
			if err := st.CreateAgent(context.Background(), row); err != nil {
				t.Fatal(err)
			}
			a := httpOutcome(t, h, http.MethodPut, "/api/agents/"+row.ID, map[string]any{"name": "must not write"})
			b := mcpOutcome(t, cs, "agent_update", map[string]any{"id": row.ID, "name": "must not write"})
			if a.Category != svcerr.CodePermission || b.Category != svcerr.CodePermission {
				t.Fatalf("want permission: %s / %s", a, b)
			}
			tp.AssertSameOutcome(t, "noneditable agent", a, b)
			after, err := st.GetAgent(context.Background(), row.ID)
			if err != nil || after.Name != row.Name {
				t.Fatalf("protected agent changed: %v / %+v", err, after)
			}
		})
	}
	a := httpOutcome(t, h, http.MethodPut, "/api/agents/missing", map[string]any{"name": "x"})
	b := mcpOutcome(t, cs, "agent_update", map[string]any{"id": "missing", "name": "x"})
	if a.Category != svcerr.CodeNotFound || b.Category != svcerr.CodeNotFound {
		t.Fatalf("want not_found: %s / %s", a, b)
	}
	tp.AssertSameOutcome(t, "missing agent", a, b)
}

func TestTransportParityInfrastructureFailure(t *testing.T) {
	st, h, cs := parityDoors(t)
	// A closed database is an infrastructure failure, even for an absent id.
	// It must never masquerade as not_found or expose the store's cause.
	st.Close(context.Background())
	a := httpOutcome(t, h, http.MethodPut, "/api/todos/missing", map[string]any{"title": "x"})
	b := mcpOutcome(t, cs, "todo_update", map[string]any{"id": "missing", "title": "x"})
	if a.Category != svcerr.CodeInternal || b.Category != svcerr.CodeInternal || a.Status != 500 {
		t.Fatalf("want internal failure: %s / %s", a, b)
	}
	tp.AssertSameOutcome(t, "closed database", a, b)
	a = httpOutcome(t, h, http.MethodPut, "/api/agents/missing", map[string]any{"name": "x"})
	b = mcpOutcome(t, cs, "agent_update", map[string]any{"id": "missing", "name": "x"})
	if a.Category != svcerr.CodeInternal || b.Category != svcerr.CodeInternal || a.Status != 500 {
		t.Fatalf("want internal failure: %s / %s", a, b)
	}
	tp.AssertSameOutcome(t, "closed database agent", a, b)
}

func TestAgentWriteFailureParityLogsCauses(t *testing.T) {
	for _, op := range []string{"create", "update"} {
		t.Run(op, func(t *testing.T) {
			st, h, cs := parityDoors(t)
			if err := st.CreateAgent(context.Background(), &store.AgentProfile{ID: "write-target", Name: "Target", Slug: "write-target", SystemPrompt: "fixture", Source: "user"}); err != nil {
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
			method, path, tool := http.MethodPost, "/api/agents", "agent_create"
			args := map[string]any{"name": "Fresh", "slug": "fresh", "system_prompt": "fixture"}
			if op == "update" {
				method, path, tool = http.MethodPut, "/api/agents/write-target", "agent_update"
				args = map[string]any{"id": "write-target", "name": "Changed"}
			}
			a := httpOutcome(t, h, method, path, args)
			b := mcpOutcome(t, cs, tool, args)
			if a.Category != svcerr.CodeInternal || b.Category != svcerr.CodeInternal {
				t.Fatalf("want internal: %s / %s", a, b)
			}
			tp.AssertSameOutcome(t, "agent write failure", a, b)
			if strings.Contains(a.Detail, "write_secret") || strings.Contains(b.Detail, "write_secret") {
				t.Fatalf("leaked write cause: %s / %s", a, b)
			}
			if strings.Count(logs.String(), "write_secret") != 2 {
				t.Fatalf("both doors must log the write cause: %s", logs.String())
			}
		})
	}
}

func TestAgentCreateClosedDBParity(t *testing.T) {
	st, h, cs := parityDoors(t)
	if err := st.DB.Close(); err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"name": "Fresh", "slug": "fresh", "system_prompt": "fixture"}
	a := httpOutcome(t, h, http.MethodPost, "/api/agents", args)
	b := mcpOutcome(t, cs, "agent_create", args)
	if a.Category != svcerr.CodeInternal || b.Category != svcerr.CodeInternal {
		t.Fatalf("want internal: %s / %s", a, b)
	}
	tp.AssertSameOutcome(t, "closed DB create", a, b)
	if strings.Contains(a.Detail, "database is closed") || strings.Contains(b.Detail, "database is closed") {
		t.Fatal("closed DB cause leaked")
	}
}

func TestTransportTodoUpdateFailureField(t *testing.T) {
	st, _, cs := parityDoors(t)
	row := &store.Todo{Title: "Original", Scope: "session", ScopeID: "fixture"}
	if err := st.CreateTodo(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	result, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "todo_update", Arguments: map[string]any{"id": row.ID, "priority": "invalid"}})
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Code  string `json:"code"`
		Field string `json:"field"`
	}
	if err := json.Unmarshal([]byte(callText(result)), &body); err != nil {
		t.Fatal(err)
	}
	if !result.IsError || body.Code != string(svcerr.CodeInvalid) || body.Field != "priority" {
		t.Fatalf("field lost at MCP door: %+v %+v", result, body)
	}
}
