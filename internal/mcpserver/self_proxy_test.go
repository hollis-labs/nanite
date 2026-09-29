package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	condmcp "github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func newProxyTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := storetest.New(t, context.Background(), t.TempDir()+"/proxy.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		s.Close(context.Background(

		// TestSelfToolProxy_ForwardsCall pins that the proxy POSTs {session_id,
		// name, args} to /api/tools/call on the configured API server and decodes
		// the ToolResult it gets back — the CLI-launch self-tools proxy contract.
		))
	})
	return s
}

func TestSelfToolProxy_ForwardsCall(t *testing.T) {
	var gotPath, gotSession, gotName string
	var gotArgs map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			SessionID string         `json:"session_id"`
			Name      string         `json:"name"`
			Args      map[string]any `json:"args"`
		}
		_ = json.Unmarshal(raw, &req)
		gotSession, gotName, gotArgs = req.SessionID, req.Name, req.Args

		json.NewEncoder(w).Encode(condmcp.ToolResult{
			Content: []condmcp.ToolContent{{Type: "text", Text: "ok:" + req.Name}},
		})
	}))
	defer srv.Close()

	p := newSelfToolProxy(newProxyTestStore(t), srv.URL, "sess-42")
	res, err := p.CallTool(context.Background(), "todo_create", map[string]any{"title": "x"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	if gotPath != "/api/tools/call" {
		t.Errorf("forwarded to %q, want /api/tools/call", gotPath)
	}
	if gotSession != "sess-42" {
		t.Errorf("session_id = %q, want sess-42", gotSession)
	}
	if gotName != "todo_create" {
		t.Errorf("name = %q, want todo_create", gotName)
	}
	if gotArgs["title"] != "x" {
		t.Errorf("args.title = %v, want x", gotArgs["title"])
	}
	if len(res.Content) != 1 || res.Content[0].Text != "ok:todo_create" {
		t.Errorf("result = %#v, want one text block ok:todo_create", res.Content)
	}
}

// TestSelfToolProxy_SurfacesHarnessError pins that a non-200 from the
// harness becomes a Go error carrying the harness's {"error": ...} message,
// so makeTransportHandler renders it as an MCP error result.
func TestSelfToolProxy_SurfacesHarnessError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "self-tools transport not available"})
	}))
	defer srv.Close()

	p := newSelfToolProxy(newProxyTestStore(t), srv.URL, "sess-1")
	_, err := p.CallTool(context.Background(), "todo_create", nil)
	if err == nil {
		t.Fatal("expected an error when the harness returns non-200")
	}
	if !strings.Contains(err.Error(), "self-tools transport not available") {
		t.Errorf("error %q should carry the harness message", err)
	}
}

// TestSelfToolProxy_ListToolsServesCatalog pins that ListTools returns the
// static self-tool catalog without contacting the API server.
func TestSelfToolProxy_ListToolsServesCatalog(t *testing.T) {
	p := newSelfToolProxy(newProxyTestStore(t), "http://127.0.0.1:1", "sess-1")
	tools, err := p.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("ListTools returned an empty catalog")
	}
}

// D-38: the proxy advertises the cache-navigation tools (the harness serves
// them) and tells the harness whether the agent can follow a cache pointer.
func TestSelfToolProxy_CacheRetrievalFlagAndCatalog(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{}})
	}))
	defer srv.Close()

	for _, tc := range []struct {
		name      string
		allowlist []string
		want      bool
	}{
		{"unrestricted CLI agent", nil, true},
		{"allowlist with both cache tools", []string{"whoami", "fetch_tool_result", "search_tool_result"}, true},
		{"allowlist with one", []string{"whoami", "fetch_tool_result"}, false},
		{"allowlist without them", []string{"whoami"}, false},
	} {
		s := New(newProxyTestStore(t), "sess-1", nil, "", srv.URL, tc.allowlist)
		got = nil
		if _, err := s.self.CallTool(context.Background(), "whoami", nil); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		flag, _ := got["cache_retrieval"].(bool)
		if flag != tc.want {
			t.Errorf("%s: cache_retrieval = %v, want %v", tc.name, flag, tc.want)
		}
	}

	p := newSelfToolProxy(newProxyTestStore(t), srv.URL, "sess-1")
	tools, _ := p.ListTools(context.Background())
	seen := map[string]bool{}
	for _, tl := range tools {
		seen[tl.Name] = true
	}
	if !seen["fetch_tool_result"] || !seen["search_tool_result"] {
		t.Error("proxy catalog does not advertise the cache-navigation tools")
	}
}
