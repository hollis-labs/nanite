package mcpclient

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

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
