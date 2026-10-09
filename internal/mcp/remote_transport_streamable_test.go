package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	gmcpclient "github.com/hollis-labs/libs/plugin-mcp/go-mcp/client"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The tests here drive remoteTransport against a real MCP server speaking the
// official Streamable HTTP transport, over a real httptest listener. Faking the SDK
// session would leave the one thing this path exists to get right —
// that a session established over /mcp works, headers and handshake
// included — unexercised.

type recordedRequest struct {
	method    string
	path      string
	header    http.Header
	rpcMethod string
}

// streamableTestServer is an httptest server running an MCP server over Streamable HTTP, plus the
// requests it saw.
type streamableTestServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []recordedRequest
}

func (s *streamableTestServer) seen() []recordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedRequest(nil), s.requests...)
}

// newStreamableTestServer starts an MCP server over Streamable HTTP with one echo tool, one tool
// that returns an error result, and one annotated read-only tool.
func newStreamableTestServer(t *testing.T) *streamableTestServer {
	t.Helper()

	srv := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "probe", Version: "0.0.1"}, nil)

	srv.AddTool(&sdkmcp.Tool{
		Name:        "echo",
		Description: "returns its argument",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"text": map[string]any{"type": "string"}},
		},
	}, func(_ context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		var args struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(req.Params.Arguments, &args)
		text := args.Text
		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "echo:" + text}},
		}, nil
	})

	srv.AddTool(&sdkmcp.Tool{
		Name:        "explode",
		Description: "always fails",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return &sdkmcp.CallToolResult{
			IsError: true,
			Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "it exploded"}},
		}, nil
	})

	srv.AddTool(&sdkmcp.Tool{
		Name:        "read_only",
		Description: "looks but does not touch",
		InputSchema: map[string]any{"type": "object"},
		Annotations: &sdkmcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "ok"}},
		}, nil
	})

	handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return srv }, nil)

	ts := &streamableTestServer{}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var message struct {
			Method string `json:"method"`
		}
		if r.Method == http.MethodPost {
			body, readErr := io.ReadAll(r.Body)
			if readErr != nil {
				http.Error(w, "fixture body", http.StatusBadRequest)
				return
			}
			_ = json.Unmarshal(body, &message)
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		ts.mu.Lock()
		ts.requests = append(ts.requests, recordedRequest{
			method:    r.Method,
			path:      r.URL.Path,
			header:    r.Header.Clone(),
			rpcMethod: message.Method,
		})
		ts.mu.Unlock()
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	t.Cleanup(ts.CloseClientConnections)
	return ts
}

// streamableTransport builds a remoteTransport wired to a fresh, single-server pool
// pointed at ts, with headers as configured. Mirrors what AddHTTPServerFromConfig
// wires in production, minus the Manager bookkeeping this package's tests
// don't need.
func streamableTransport(t *testing.T, ts *streamableTestServer, headers map[string]string) *remoteTransport {
	t.Helper()
	m := NewManager()
	t.Cleanup(m.Close)
	if err := m.AddHTTPServerWithHeaders("probe", ts.URL+"/mcp", headers, TierThirdPartyHTTP); err != nil {
		t.Fatal(err)
	}
	m.mu.RLock()
	transport := m.servers["probe"].(*remoteTransport)
	m.mu.RUnlock()
	return transport
}

func TestRemoteTransportStreamable_ListsTools(t *testing.T) {
	ts := newStreamableTestServer(t)
	transport := streamableTransport(t, ts, nil)

	tools, err := transport.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	byName := make(map[string]Tool, len(tools))
	for _, tool := range tools {
		byName[tool.Name] = tool
	}
	for _, want := range []string{"echo", "explode", "read_only"} {
		if _, ok := byName[want]; !ok {
			t.Fatalf("ListTools returned %v, missing %q", byName, want)
		}
	}
	if got := byName["echo"].Description; got != "returns its argument" {
		t.Errorf("echo description = %q", got)
	}
	if got := byName["echo"].InputSchema["type"]; got != "object" {
		t.Errorf("echo input schema type = %v, want object — the schema did not survive conversion", got)
	}
}

// Annotations are what an approval gate keys off (Manager.ToolBehavior), so a
// conversion that dropped them would downgrade a write to "unknown" silently.
func TestRemoteTransportStreamable_CarriesToolAnnotations(t *testing.T) {
	ts := newStreamableTestServer(t)
	transport := streamableTransport(t, ts, nil)

	tools, err := transport.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	var readOnly, echo Tool
	for _, tool := range tools {
		switch tool.Name {
		case "read_only":
			readOnly = tool
		case "echo":
			echo = tool
		}
	}

	if readOnly.Annotations["readOnlyHint"] != true {
		t.Errorf("read_only annotations = %v, want readOnlyHint true", readOnly.Annotations)
	}
	if len(echo.Annotations) != 0 {
		t.Errorf("echo declared no annotations but got %v — an invented hint is worse than none", echo.Annotations)
	}
}

func TestRemoteTransportStreamable_CallsTool(t *testing.T) {
	ts := newStreamableTestServer(t)
	transport := streamableTransport(t, ts, nil)

	result, err := transport.CallTool(context.Background(), "echo", map[string]any{"text": "hi"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("CallTool returned IsError for a successful call: %+v", result)
	}
	if len(result.Content) != 1 || result.Content[0].Type != "text" || result.Content[0].Text != "echo:hi" {
		t.Fatalf("CallTool content = %+v, want one text block %q", result.Content, "echo:hi")
	}
}

// A tool-level error is a result with IsError, not a transport error. Turning
// it into one would hide the message the model needs to self-correct.
func TestRemoteTransportStreamable_SurfacesToolErrorAsResult(t *testing.T) {
	ts := newStreamableTestServer(t)
	transport := streamableTransport(t, ts, nil)

	result, err := transport.CallTool(context.Background(), "explode", nil)
	if err != nil {
		t.Fatalf("CallTool returned a transport error for a tool-level failure: %v", err)
	}
	if !result.IsError {
		t.Fatalf("CallTool result IsError = false, want true: %+v", result)
	}
	if len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, "it exploded") {
		t.Fatalf("CallTool content = %+v, want the server's error text", result.Content)
	}
}

// Streamable HTTP carries configured authentication and identity headers on
// every request to the same verified endpoint, including initialization.
func TestRemoteTransportStreamable_SendsConfiguredHeadersOnEveryRequest(t *testing.T) {
	ts := newStreamableTestServer(t)
	transport := streamableTransport(t, ts, map[string]string{
		"Authorization":           "Bearer probe-token",
		"X-Forwarded-User-Email":  "someone@example.com",
		"x-lowercase-header-name": "canonicalized",
	})

	if _, err := transport.CallTool(context.Background(), "echo", map[string]any{"text": "hi"}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	seen := ts.seen()
	if len(seen) < 2 {
		t.Fatalf("server saw %d requests, want the handshake and tool-call POSTs: %+v", len(seen), seen)
	}
	var sawPOST bool
	for _, req := range seen {
		switch req.method {
		case http.MethodPost:
			sawPOST = true
		}
		if req.path != "/mcp" {
			t.Fatalf("request reached unexpected endpoint %q", req.path)
		}
		if got := req.header.Get("Authorization"); got != "Bearer probe-token" {
			t.Errorf("%s %s Authorization = %q, want the configured bearer token", req.method, req.path, got)
		}
		if got := req.header.Get("X-Forwarded-User-Email"); got != "someone@example.com" {
			t.Errorf("%s %s X-Forwarded-User-Email = %q — identity did not reach the server",
				req.method, req.path, got)
		}
		if got := req.header.Get("X-Lowercase-Header-Name"); got != "canonicalized" {
			t.Errorf("%s %s X-Lowercase-Header-Name = %q, want the value set under its lowercase spelling",
				req.method, req.path, got)
		}
	}
	if !sawPOST {
		t.Fatal("Streamable transport made no POST")
	}
}

// Header maps come from ParseHeaderJSON, which is how the stored column
// reaches the transport. Wiring them separately in each transport is how
// AddHTTPServerWithHeaders came to sit uncalled, so this pins the seam.
func TestRemoteTransportStreamable_TakesHeadersFromStoredJSON(t *testing.T) {
	ts := newStreamableTestServer(t)
	headers, err := ParseHeaderJSON(`{"Authorization":"Bearer stored","X-Forwarded-User-Email":"stored@example.com"}`)
	if err != nil {
		t.Fatalf("ParseHeaderJSON: %v", err)
	}
	transport := streamableTransport(t, ts, headers)

	if _, err := transport.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, req := range ts.seen() {
		if got := req.header.Get("Authorization"); got != "Bearer stored" {
			t.Fatalf("%s %s Authorization = %q, want the value from the stored headers column", req.method, req.path, got)
		}
	}
}

func TestRemoteTransportStreamable_FailsOnAnEndpointThatIsNotAnMCPServer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusNotFound)
	}))
	t.Cleanup(ts.Close)

	pool := gmcpclient.NewPool()
	t.Cleanup(func() { _ = pool.Close() })
	if err := pool.Register("probe", gmcpclient.ServerConfig{Transport: gmcpclient.TransportHTTP, URL: ts.URL}); err != nil {
		t.Fatalf("pool.Register: %v", err)
	}
	transport := newRemoteTransport(pool, "probe", gmcpclient.TransportHTTP)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := transport.ListTools(ctx); err == nil {
		t.Fatal("ListTools against a non-MCP endpoint succeeded")
	}
}

// Manager sets this from the registered server's trust tier; the wiring has
// to reach the underlying client, not just be accepted and ignored.
func TestRemoteTransportStreamable_TierCapAppliesToTheConnection(t *testing.T) {
	ts := newStreamableTestServer(t)
	transport := streamableTransport(t, ts, nil)

	// A cap far below any real handshake event.
	transport.SetMaxResponseBytes(8)

	if _, err := transport.ListTools(context.Background()); err == nil {
		t.Fatal("ListTools succeeded with an 8-byte cap — the cap never reached the connection")
	}
}

// Manager wires the tier ceiling and restart/removal through these
// interfaces; losing one would silently leave a remote server unmanaged.
func TestRemoteTransport_ImplementsManagerInterfaces(t *testing.T) {
	pool := gmcpclient.NewPool()
	t.Cleanup(func() { _ = pool.Close() })
	var transport any = newRemoteTransport(pool, "probe", gmcpclient.TransportHTTP)
	if _, ok := transport.(MCPTransport); !ok {
		t.Error("remoteTransport does not implement MCPTransport")
	}
	if _, ok := transport.(interface{ SetMaxResponseBytes(int) }); !ok {
		t.Error("remoteTransport does not implement SetMaxResponseBytes")
	}
	if _, ok := transport.(interface{ Close() error }); !ok {
		t.Error("remoteTransport does not implement Close")
	}
}

// A stateful Streamable endpoint requires initialization and the same session
// on subsequent calls; a JSON-only POST adapter cannot satisfy this fixture.
func TestRemoteTransportStreamable_StatefulSessionWithoutLegacyEndpoint(t *testing.T) {
	ts := newStreamableTestServer(t)
	transport := streamableTransport(t, ts, nil)
	if _, err := transport.ListTools(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := transport.CallTool(context.Background(), "echo", map[string]any{"text": "session"}); err != nil {
		t.Fatal(err)
	}
	initialized := 0
	session := ""
	for _, req := range ts.seen() {
		if req.path != "/mcp" {
			t.Fatalf("legacy or guessed endpoint reached: %s", req.path)
		}
		if req.rpcMethod == "initialize" {
			initialized++
			continue
		}
		if req.rpcMethod != "tools/list" && req.rpcMethod != "tools/call" {
			continue
		}
		id := req.header.Get("Mcp-Session-Id")
		if id == "" {
			t.Fatal("stateful request omitted session ID")
		}
		if session != "" && id != session {
			t.Fatal("session changed between list and call")
		}
		session = id
	}
	if initialized != 1 || session == "" {
		t.Fatalf("missing initialized session: %d %q", initialized, session)
	}
}

func TestRemoteTransportStreamable_NoLegacyFallback(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		http.Error(w, "unsupported endpoint", http.StatusNotFound)
	}))
	t.Cleanup(ts.Close)
	m := NewManager()
	t.Cleanup(m.Close)
	if err := m.AddHTTPServer("probe", ts.URL+"/mcp", TierThirdPartyHTTP); err != nil {
		t.Fatal(err)
	}
	m.mu.RLock()
	transport := m.servers["probe"].(*remoteTransport)
	m.mu.RUnlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := transport.ListTools(ctx); err == nil {
		t.Fatal("non-MCP endpoint succeeded")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) == 0 {
		t.Fatal("fixture saw no initialization request")
	}
	for _, path := range paths {
		if path != "/mcp" {
			t.Fatalf("transport guessed a fallback endpoint: %s", path)
		}
	}
}
