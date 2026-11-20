package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/libs/agent/agentdir"
	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/nanite/internal/tools"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func testServer(text string) *sdk.Server {
	server := sdk.NewServer(&sdk.Implementation{Name: "fixture", Version: "1"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "hello", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}},
		func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}, nil, nil
		})
	return server
}

func TestMCPTransports(t *testing.T) {
	if os.Getenv("NANITE_MCP_TEST_SERVER") == "1" {
		text := os.Getenv("NANITE_MCP_TEST_DECLARED") + ":" + os.Getenv("NANITE_MCP_UNDECLARED")
		if err := testServer(text).Run(t.Context(), &sdk.StdioTransport{}); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	t.Setenv("NANITE_MCP_TEST_VALUE", "declared")
	t.Setenv("NANITE_MCP_UNDECLARED", "private")
	streamable := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return testServer("streamable") }, nil))
	defer streamable.Close()
	sse := httptest.NewServer(sdk.NewSSEHandler(func(*http.Request) *sdk.Server { return testServer("sse") }, nil))
	defer sse.Close()
	client := New(t.TempDir(), nil, redact.NewScope(), slog.Default())
	defer client.Close()
	lease, warnings := client.Reconcile(t.Context(), agentdir.MCPConfig{Servers: map[string]agentdir.MCPServer{
		"stdio": {Type: "stdio", Command: os.Args[0], Args: []string{"-test.run=^TestMCPTransports$"}, Env: map[string]string{
			"NANITE_MCP_TEST_SERVER": "1", "NANITE_MCP_TEST_DECLARED": "${NANITE_MCP_TEST_VALUE}",
		}},
		"streamable": {Type: "http", URL: streamable.URL},
		"sse":        {Type: "sse", URL: sse.URL},
	}})
	defer lease.Close()
	got, discover := lease.Tools(t.Context())
	if len(warnings)+len(discover) > 0 || len(got) != 3 {
		t.Fatalf("tools %d, warnings %v %v", len(got), warnings, discover)
	}
	for _, tool := range got {
		result, err := tool.Handler(t.Context(), tools.Call{Input: json.RawMessage("{}")})
		server := strings.Split(tool.Name, "__")[1]
		want := server
		if server == "stdio" {
			want = "declared:"
		}
		if err != nil || result.Text != want || !tool.ReadOnly || tool.Destructive {
			t.Fatalf("%s = %+v, %v", tool.Name, result, err)
		}
	}
}

func TestReconcileKeepsActiveTurnConnections(t *testing.T) {
	local := func(text string) *httptest.Server {
		server := testServer(text)
		return httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil))
	}
	first, second := local("first"), local("second")
	defer first.Close()
	defer second.Close()
	client := New(t.TempDir(), nil, redact.NewScope(), slog.Default())
	defer client.Close()
	acquire := func(url string) *Lease {
		t.Helper()
		lease, warnings := client.Reconcile(t.Context(), agentdir.MCPConfig{Servers: map[string]agentdir.MCPServer{"fixture": {Type: "http", URL: url}}})
		if len(warnings) > 0 {
			t.Fatal(warnings)
		}
		t.Cleanup(lease.Close)
		return lease
	}
	old := acquire(first.URL)
	same := acquire(first.URL)
	if old.servers[0] != same.servers[0] {
		t.Fatal("unchanged server was replaced")
	}
	same.Close()
	current := acquire(second.URL)
	for _, item := range []struct {
		lease *Lease
		want  string
	}{{old, "first"}, {current, "second"}} {
		got, warnings := item.lease.Tools(t.Context())
		if len(warnings) > 0 || len(got) != 1 {
			t.Fatalf("tools %d, warnings %v", len(got), warnings)
		}
		result, err := got[0].Handler(t.Context(), tools.Call{Input: json.RawMessage("{}")})
		if err != nil || result.Text != item.want {
			t.Fatalf("result %+v, %v", result, err)
		}
	}
	key := old.servers[0].key
	old.Close()
	if _, err := client.pool.Get(key); err == nil {
		t.Fatal("retired registration still exists")
	}
	empty, _ := client.Reconcile(t.Context(), agentdir.MCPConfig{})
	defer empty.Close()
	if got, _ := empty.Tools(t.Context()); len(got) != 0 {
		t.Fatalf("removed servers yielded %d tools", len(got))
	}
}

func TestToolCacheRefreshAfterUnknownTool(t *testing.T) {
	server := testServer("original")
	var lists atomic.Int32
	server.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
			if method == "tools/list" {
				lists.Add(1)
			}
			return next(ctx, method, req)
		}
	})
	web := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{Stateless: true}))
	defer web.Close()
	client := New(t.TempDir(), nil, redact.NewScope(), slog.Default())
	defer client.Close()
	cfg := agentdir.MCPConfig{Servers: map[string]agentdir.MCPServer{"fixture": {Type: "http", URL: web.URL}}}
	lease, warnings := client.Reconcile(t.Context(), cfg)
	defer lease.Close()
	first, discoveryWarnings := lease.Tools(t.Context())
	if len(warnings)+len(discoveryWarnings) > 0 || len(first) != 1 {
		t.Fatalf("initial discovery: %v %v", warnings, discoveryWarnings)
	}
	again, _ := client.Reconcile(t.Context(), cfg)
	defer again.Close()
	if got, warnings := again.Tools(t.Context()); len(got) != 1 || len(warnings) > 0 || lists.Load() != 1 {
		t.Fatalf("cached discovery: lists=%d tools=%d warnings=%v", lists.Load(), len(got), warnings)
	}
	server.RemoveTools("hello")
	if _, err := first[0].Handler(t.Context(), tools.Call{Input: json.RawMessage("{}")}); !isUnknownTool(err) {
		t.Fatalf("expected unknown tool, got %v", err)
	}
	if got, warnings := again.Tools(t.Context()); len(got) != 0 || len(warnings) > 0 || lists.Load() != 2 {
		t.Fatalf("refreshed discovery: lists=%d tools=%d warnings=%v", lists.Load(), len(got), warnings)
	}
}

func TestToolCacheRefreshAfterSSENotification(t *testing.T) {
	frames := make(chan string, 8)
	var lists atomic.Int32
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/event-stream")
			controller := http.NewResponseController(w)
			_, _ = fmt.Fprint(w, "event: endpoint\ndata: /messages\n\n")
			_ = controller.Flush()
			for {
				select {
				case frame := <-frames:
					_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", frame)
					if err := controller.Flush(); err != nil {
						return
					}
				case <-r.Context().Done():
					return
				}
			}
		}
		var request struct {
			ID     json.RawMessage
			Method string
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			w.WriteHeader(400)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		if len(request.ID) == 0 {
			return
		}
		var reply string
		switch request.Method {
		case "initialize":
			reply = `"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":true}},"serverInfo":{"name":"fixture","version":"1"}}`
		case "tools/list":
			reply = `"result":{"tools":[]}`
			if lists.Add(1) == 1 {
				reply = `"result":{"tools":[{"name":"hello","inputSchema":{"type":"object"}}]}`
			}
		default:
			reply = `"error":{"code":-32601,"message":"unsupported method"}`
		}
		frames <- fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,%s}`, request.ID, reply)
	}))
	defer web.Close()
	client := New(t.TempDir(), nil, redact.NewScope(), slog.Default())
	defer client.Close()
	lease, warnings := client.Reconcile(t.Context(), agentdir.MCPConfig{Servers: map[string]agentdir.MCPServer{"fixture": {Type: "sse", URL: web.URL}}})
	defer lease.Close()
	if got, w := lease.Tools(t.Context()); len(got) != 1 || len(warnings)+len(w) > 0 {
		t.Fatalf("initial discovery: %v %v", warnings, w)
	}
	frames <- `{"jsonrpc":"2.0","method":"notifications/tools/list_changed","params":{}}`
	deadline := time.Now().Add(2 * time.Second)
	for lease.servers[0].generation.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("tool list change notification did not arrive")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got, w := lease.Tools(t.Context()); len(got) != 0 || len(w) > 0 || lists.Load() != 2 {
		t.Fatalf("refreshed discovery: lists=%d tools=%d warnings=%v", lists.Load(), len(got), w)
	}
}

func TestDiscoveryStartsServersConcurrently(t *testing.T) {
	entered := make(chan struct{}, 2)
	leave := make(chan struct{})
	cfg := agentdir.MCPConfig{Servers: make(map[string]agentdir.MCPServer)}
	for _, name := range []string{"first", "second"} {
		server := testServer(name)
		server.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
			return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
				if method == "tools/list" {
					entered <- struct{}{}
					select {
					case <-leave:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				return next(ctx, method, req)
			}
		})
		web := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil))
		defer web.Close()
		cfg.Servers[name] = agentdir.MCPServer{Type: "http", URL: web.URL}
	}
	client := New(t.TempDir(), nil, redact.NewScope(), slog.Default())
	defer client.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	lease, warnings := client.Reconcile(ctx, cfg)
	defer lease.Close()
	if len(warnings) > 0 {
		t.Fatal(warnings)
	}
	result := make(chan int, 1)
	go func() { got, warnings := lease.Tools(ctx); result <- len(got) - len(warnings) }()
	for range 2 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("both discovery calls did not start concurrently")
		}
	}
	close(leave)
	if got := <-result; got != 2 {
		t.Fatalf("discovery result = %d", got)
	}
}

func TestCyclicToolSchemaLeavesOnlyThatToolOut(t *testing.T) {
	server := testServer("ok")
	// Validating against this schema would recurse until the stack overflows.
	server.AddTool(&sdk.Tool{Name: "loop", InputSchema: json.RawMessage(`{"type":"object","allOf":[{"$ref":"#"}]}`)},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{}, nil
		})
	httpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil))
	defer httpServer.Close()
	client := New(t.TempDir(), nil, redact.NewScope(), slog.Default())
	defer client.Close()
	lease, warnings := client.Reconcile(t.Context(), agentdir.MCPConfig{Servers: map[string]agentdir.MCPServer{"fixture": {Type: "http", URL: httpServer.URL}}})
	if len(warnings) > 0 {
		t.Fatal(warnings)
	}
	defer lease.Close()
	got, discover := lease.Tools(t.Context())
	if len(got) != 1 || got[0].Name != "mcp__fixture__hello" {
		t.Fatalf("tools %+v", got)
	}
	if len(discover) != 1 || !strings.Contains(discover[0], `MCP server "fixture"`) || !strings.Contains(discover[0], `tool "loop" left out`) ||
		!strings.Contains(discover[0], "refers back to itself") {
		t.Fatalf("warnings %v", discover)
	}
}
