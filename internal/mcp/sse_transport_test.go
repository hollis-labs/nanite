package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The tests here drive the adapter against a real MCP server speaking the real
// HTTP+SSE transport, over a real httptest listener. Faking the SDK session
// would leave the one thing this type exists to get right — that a session
// established over /sse works at all, headers and handshake included —
// unexercised.

type recordedRequest struct {
	method string
	path   string
	header http.Header
}

// sseTestServer is an httptest server running an MCP server over SSE, plus the
// requests it saw.
type sseTestServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []recordedRequest
}

func (s *sseTestServer) seen() []recordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedRequest(nil), s.requests...)
}

// newSSETestServer starts an MCP server over SSE with one echo tool, one tool
// that returns an error result, and one annotated read-only tool.
func newSSETestServer(t *testing.T) *sseTestServer {
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

	handler := sdkmcp.NewSSEHandler(func(*http.Request) *sdkmcp.Server { return srv }, nil)

	ts := &sseTestServer{}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ts.mu.Lock()
		ts.requests = append(ts.requests, recordedRequest{
			method: r.Method,
			path:   r.URL.Path,
			header: r.Header.Clone(),
		})
		ts.mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestSSETransportListsTools(t *testing.T) {
	ts := newSSETestServer(t)
	transport := NewSSETransport(ts.URL, nil)
	t.Cleanup(func() { _ = transport.Close() })

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
func TestSSETransportCarriesToolAnnotations(t *testing.T) {
	ts := newSSETestServer(t)
	transport := NewSSETransport(ts.URL, nil)
	t.Cleanup(func() { _ = transport.Close() })

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

func TestSSETransportCallsTool(t *testing.T) {
	ts := newSSETestServer(t)
	transport := NewSSETransport(ts.URL, nil)
	t.Cleanup(func() { _ = transport.Close() })

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
func TestSSETransportSurfacesToolErrorAsResult(t *testing.T) {
	ts := newSSETestServer(t)
	transport := NewSSETransport(ts.URL, nil)
	t.Cleanup(func() { _ = transport.Close() })

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

// The whole reason this transport exists: some gateways forward identity
// headers upstream only over /sse, so they have to be on every request — the
// long-lived GET and the message POSTs alike.
func TestSSETransportSendsConfiguredHeadersOnEveryRequest(t *testing.T) {
	ts := newSSETestServer(t)
	transport := NewSSETransport(ts.URL, map[string]string{
		"Authorization":           "Bearer probe-token",
		"X-Forwarded-User-Email":  "someone@example.com",
		"x-lowercase-header-name": "canonicalized",
	})
	t.Cleanup(func() { _ = transport.Close() })

	if _, err := transport.CallTool(context.Background(), "echo", map[string]any{"text": "hi"}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	seen := ts.seen()
	if len(seen) < 2 {
		t.Fatalf("server saw %d requests, want the SSE GET and at least one message POST: %+v", len(seen), seen)
	}
	var sawGET, sawPOST bool
	for _, req := range seen {
		switch req.method {
		case http.MethodGet:
			sawGET = true
		case http.MethodPost:
			sawPOST = true
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
	if !sawGET || !sawPOST {
		t.Fatalf("expected both an SSE GET and a message POST, saw GET=%v POST=%v", sawGET, sawPOST)
	}
}

// Header maps come from ParseHeaderJSON, which is how the stored column
// reaches the transport. Wiring them separately in each transport is how
// AddHTTPServerWithHeaders came to sit uncalled, so this pins the seam.
func TestSSETransportTakesHeadersFromStoredJSON(t *testing.T) {
	ts := newSSETestServer(t)
	headers, err := ParseHeaderJSON(`{"Authorization":"Bearer stored","X-Forwarded-User-Email":"stored@example.com"}`)
	if err != nil {
		t.Fatalf("ParseHeaderJSON: %v", err)
	}
	transport := NewSSETransport(ts.URL, headers)
	t.Cleanup(func() { _ = transport.Close() })

	if _, err := transport.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, req := range ts.seen() {
		if got := req.header.Get("Authorization"); got != "Bearer stored" {
			t.Fatalf("%s %s Authorization = %q, want the value from the stored headers column", req.method, req.path, got)
		}
	}
}

// Mutating the caller's map after construction must not change what is sent.
func TestSSETransportCopiesHeaderMap(t *testing.T) {
	ts := newSSETestServer(t)
	headers := map[string]string{"Authorization": "Bearer original"}
	transport := NewSSETransport(ts.URL, headers)
	t.Cleanup(func() { _ = transport.Close() })
	headers["Authorization"] = "Bearer swapped"

	if _, err := transport.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, req := range ts.seen() {
		if got := req.header.Get("Authorization"); got != "Bearer original" {
			t.Fatalf("Authorization = %q, want the value captured at construction", got)
		}
	}
}

// The session is established once and reused: a second call must not open a
// second event stream.
func TestSSETransportReusesTheSession(t *testing.T) {
	ts := newSSETestServer(t)
	transport := NewSSETransport(ts.URL, nil)
	t.Cleanup(func() { _ = transport.Close() })

	ctx := context.Background()
	if _, err := transport.ListTools(ctx); err != nil {
		t.Fatalf("first ListTools: %v", err)
	}
	if _, err := transport.CallTool(ctx, "echo", map[string]any{"text": "again"}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	gets := 0
	for _, req := range ts.seen() {
		if req.method == http.MethodGet {
			gets++
		}
	}
	if gets != 1 {
		t.Fatalf("server saw %d SSE GETs, want 1 — the session is being re-established per call", gets)
	}
}

// Losing the stream must cost a reconnect, not a permanently dead server.
func TestSSETransportReconnectsAfterTheStreamDrops(t *testing.T) {
	ts := newSSETestServer(t)
	transport := NewSSETransport(ts.URL, nil)
	t.Cleanup(func() { _ = transport.Close() })

	ctx := context.Background()
	if _, err := transport.ListTools(ctx); err != nil {
		t.Fatalf("first ListTools: %v", err)
	}

	// Kill the live session the way a proxy idle timeout would: the stream
	// ends, and the transport finds out through ClientSession.Wait.
	transport.mu.Lock()
	sess := transport.sess
	transport.mu.Unlock()
	if sess == nil {
		t.Fatal("no session after a successful call")
	}
	sess.cancel()
	select {
	case <-sess.dead:
	case <-time.After(5 * time.Second):
		t.Fatal("session was not observed dead within 5s of the stream ending")
	}

	if _, err := transport.ListTools(ctx); err != nil {
		t.Fatalf("ListTools after the stream dropped: %v", err)
	}

	transport.mu.Lock()
	replacement := transport.sess
	transport.mu.Unlock()
	if replacement == nil || replacement == sess {
		t.Fatal("transport did not establish a replacement session")
	}
}

func TestSSETransportFailsOnAnEndpointThatIsNotAnMCPServer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusNotFound)
	}))
	t.Cleanup(ts.Close)

	transport := NewSSETransport(ts.URL, nil)
	t.Cleanup(func() { _ = transport.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := transport.ListTools(ctx); err == nil {
		t.Fatal("ListTools against a non-MCP endpoint succeeded")
	}
}

func TestEventCapReaderResetsAtEachEventBoundary(t *testing.T) {
	// Three small events, well over the cap in total but not individually.
	stream := "data: aaaaaaaa\n\ndata: bbbbbbbb\n\ndata: cccccccc\n\n"
	r := &eventCapReader{inner: readCloser{strings.NewReader(stream)}, max: 20}
	buf := make([]byte, 7) // deliberately not aligned to event boundaries
	var got strings.Builder
	for {
		n, err := r.Read(buf)
		got.Write(buf[:n])
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("Read: %v", err)
		}
	}
	if got.String() != stream {
		t.Fatalf("stream came through as %q", got.String())
	}
}

func TestEventCapReaderRejectsAnOversizedEvent(t *testing.T) {
	stream := "data: " + strings.Repeat("x", 100) + "\n\n"
	r := &eventCapReader{inner: readCloser{strings.NewReader(stream)}, max: 20}
	buf := make([]byte, 64)
	var err error
	for err == nil {
		_, err = r.Read(buf)
	}
	if !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("Read error = %v, want the per-event cap to reject the event", err)
	}
}

func TestSSETransportTierCapAppliesToTheEventStream(t *testing.T) {
	ts := newSSETestServer(t)
	transport := NewSSETransport(ts.URL, nil)
	t.Cleanup(func() { _ = transport.Close() })

	// A cap far below any real handshake event. Manager sets this from the
	// registered server's trust tier, so the wiring has to reach the stream.
	transport.SetMaxResponseBytes(8)

	if _, err := transport.ListTools(context.Background()); err == nil {
		t.Fatal("ListTools succeeded with an 8-byte per-event cap — the cap never reached the stream")
	}
}

// Manager wires the tier ceiling through this interface; losing the method
// would silently leave a remote server uncapped.
func TestSSETransportImplementsTheManagerInterfaces(t *testing.T) {
	var transport any = NewSSETransport("http://example.invalid/sse", nil)
	if _, ok := transport.(MCPTransport); !ok {
		t.Error("SSETransport does not implement MCPTransport")
	}
	if _, ok := transport.(interface{ SetMaxResponseBytes(int) }); !ok {
		t.Error("SSETransport does not implement SetMaxResponseBytes")
	}
	if _, ok := transport.(interface{ Close() error }); !ok {
		t.Error("SSETransport does not implement Close")
	}
}

func TestConvertSDKCallResultKeepsNonTextContent(t *testing.T) {
	res := &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: "before"},
			&sdkmcp.ImageContent{Data: []byte("pretend-png"), MIMEType: "image/png"},
		},
	}
	got := convertSDKCallResult(res)
	if len(got.Content) != 2 {
		t.Fatalf("content = %+v, want both blocks — a dropped block reaches the agent as a shorter answer, not as a gap", got.Content)
	}
	if got.Content[0].Text != "before" {
		t.Errorf("first block = %+v", got.Content[0])
	}
	if !strings.Contains(got.Content[1].Text, "image/png") {
		t.Errorf("second block = %+v, want the image block's JSON", got.Content[1])
	}
}

// A structured-only result has its payload nowhere else; the agent surface is
// text, so it has to be rendered rather than returned as an empty success.
func TestConvertSDKCallResultRendersStructuredOnlyResults(t *testing.T) {
	res := &sdkmcp.CallToolResult{StructuredContent: map[string]any{"employee_id": "42"}}
	got := convertSDKCallResult(res)
	if len(got.Content) != 1 {
		t.Fatalf("content = %+v, want one rendered block", got.Content)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(got.Content[0].Text), &decoded); err != nil {
		t.Fatalf("rendered block is not JSON: %q", got.Content[0].Text)
	}
	if decoded["employee_id"] != "42" {
		t.Errorf("rendered block = %q", got.Content[0].Text)
	}
}

type readCloser struct{ *strings.Reader }

func (readCloser) Close() error { return nil }

// A user abandoning a turn says nothing about the stream's health. Tearing the
// session down there would make every canceled turn cost the next one a
// reconnect.
func TestSSETransportKeepsTheSessionWhenTheCallerCancels(t *testing.T) {
	ts := newSSETestServer(t)
	transport := NewSSETransport(ts.URL, nil)
	t.Cleanup(func() { _ = transport.Close() })

	if _, err := transport.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	transport.mu.Lock()
	sess := transport.sess
	transport.mu.Unlock()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := transport.CallTool(canceled, "echo", map[string]any{"text": "hi"}); err == nil {
		t.Fatal("CallTool with a canceled context succeeded")
	}

	transport.mu.Lock()
	after := transport.sess
	transport.mu.Unlock()
	if after != sess {
		t.Fatal("the session was retired because the caller gave up, not because the stream died")
	}
}
