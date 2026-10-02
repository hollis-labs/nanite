package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	condmcp "github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/storetest"
)

// forwardRecorder is a stand-in for the harness's POST /api/tools/call: it
// records every forwarded call and answers each with "ok:<name>".
type forwardRecorder struct {
	mu    sync.Mutex
	calls []map[string]any
}

func (f *forwardRecorder) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.calls = append(f.calls, req)
		f.mu.Unlock()
		name, _ := req["name"].(string)
		_ = json.NewEncoder(w).Encode(condmcp.ToolResult{
			Content: []condmcp.ToolContent{{Type: "text", Text: "ok:" + name}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *forwardRecorder) names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var names []string
	for _, c := range f.calls {
		name, _ := c["name"].(string)
		names = append(names, name)
	}
	return names
}

func listedSorted(t *testing.T, srv *Server) []string {
	t.Helper()
	res, err := connectClient(t, srv).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := toolNames(res.Tools)
	slices.Sort(names)
	return names
}

func callText(res *mcp.CallToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

// The tools a forwarding server advertises are pinned to what each launch
// advertised before CW-20261001-0188 moved it off the database:
//   - ScopeStore (subagent, background, one-shot launches): exactly what a
//     local `nanite mcp` over a bare store lists;
//   - ScopeHarness (chat launches): every self tool, the harness's cache
//     navigation tools and the dev tools, as the API-URL proxy listed them.
func TestForwardingServer_AdvertisesTodaysSetPerScope(t *testing.T) {
	t.Run("store scope lists the bare-store set", func(t *testing.T) {
		want := listedSorted(t, newAllowlistedTestServer(t, nil))
		got := listedSorted(t, NewForwarding("s1", nil, "", "http://127.0.0.1:1", ScopeStore, nil))
		if !slices.Equal(got, want) {
			t.Errorf("store scope lists %v\nlocal bare-store server lists %v", got, want)
		}
	})

	t.Run("harness scope lists the full catalog", func(t *testing.T) {
		s, err := storetest.New(t, context.Background(), t.TempDir()+"/test.db")
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		t.Cleanup(func() { s.Close(context.Background()) })
		var want []string
		self, _ := service.NewSelfToolsTransport(s).ListTools(context.Background())
		dev, _ := condmcp.NewDevToolsTransport(nil).ListTools(context.Background())
		for _, tool := range append(self, dev...) {
			want = append(want, tool.Name)
		}
		want = append(want, "fetch_tool_result", "search_tool_result")
		slices.Sort(want)

		got := listedSorted(t, NewForwarding("s1", nil, "", "http://127.0.0.1:1", ScopeHarness, nil))
		if !slices.Equal(got, want) {
			t.Errorf("harness scope lists %v\nwant %v", got, want)
		}
	})
}

// A store-scoped server forwards the tools it advertises and answers the
// rest itself: a call naming a harness-only tool never reaches the harness,
// so routing a non-chat launch through the API grants it nothing.
func TestForwardingServer_StoreScopeForwardsOnlyItsSet(t *testing.T) {
	rec := &forwardRecorder{}
	api := rec.server(t)
	cs := connectClient(t, NewForwarding("sub-1", nil, "", api.URL, ScopeStore, nil))
	ctx := context.Background()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "agent_list", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool(agent_list): %v", err)
	}
	if res.IsError || callText(res) != "ok:agent_list" {
		t.Fatalf("agent_list = IsError %v %q, want the harness's answer", res.IsError, callText(res))
	}

	for _, name := range []string{"subagent_spawn", "message_send", "task_execute", "todo_create"} {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
		if err != nil {
			t.Fatalf("CallTool(%s): %v", name, err)
		}
		if !res.IsError || !strings.Contains(callText(res), "not available to this launch") {
			t.Errorf("%s = IsError %v %q, want refused locally", name, res.IsError, callText(res))
		}
	}
	if got := rec.names(); !slices.Equal(got, []string{"agent_list"}) {
		t.Errorf("forwarded %v, want only agent_list", got)
	}
	rec.mu.Lock()
	first := rec.calls[0]
	rec.mu.Unlock()
	if first["session_id"] != "sub-1" {
		t.Errorf("forwarded session_id = %v, want sub-1", first["session_id"])
	}
	if _, set := first["cache_retrieval"]; set {
		t.Errorf("store scope advertises no cache navigation, so must not offer cache_retrieval: %v", first)
	}
}

// A forwarding server has no store, so dev_read(artifact_id=...) says the
// lookup is not available here rather than failing obscurely.
func TestForwardingServer_DevReadArtifactIDUnavailable(t *testing.T) {
	cs := connectClient(t, NewForwarding("s1", nil, t.TempDir(), "http://127.0.0.1:1", ScopeHarness, nil))
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "dev_read",
		Arguments: map[string]any{"artifact_id": "art-stash-1"},
	})
	if err != nil {
		t.Fatalf("CallTool(dev_read): %v", err)
	}
	if !res.IsError || !strings.Contains(callText(res), "not available in this launch mode") {
		t.Errorf("dev_read(artifact_id) = IsError %v %q, want not available in this launch mode", res.IsError, callText(res))
	}
}
