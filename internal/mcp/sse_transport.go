package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/internal/version"
)

// sseCallTimeout bounds a single tools/list or tools/call round trip. It
// mirrors HTTPTransport's 60s http.Client timeout, which is the only per-call
// bound the manager relies on — ExecuteTool passes the caller's context
// through without adding one of its own.
const sseCallTimeout = 60 * time.Second

// sseConnectTimeout bounds the connect handshake — the SSE GET, the endpoint
// event, initialize, and notifications/initialized. Short, for the same reason
// StdioTransport's handshake timeout is: a URL that is not an MCP server never
// answers, and discovery must fail rather than hang.
const sseConnectTimeout = 30 * time.Second

// sseKeepAliveInterval makes the SDK ping the server periodically and close
// the session when the peer stops answering. Without it a stream that died
// silently — a proxy idle timeout, which is the normal way a long-lived SSE
// connection ends — is only discovered by the next tool call failing.
const sseKeepAliveInterval = 30 * time.Second

// sseKeepAliveFailures tolerates one missed ping before tearing the session
// down, so a single blip does not cost a reconnect.
const sseKeepAliveFailures = 2

// maxSSEEventBytes caps a single SSE event, the same 10 MiB safety net
// HTTPTransport and StdioTransport apply to one response body / one line.
const maxSSEEventBytes = 10 * 1024 * 1024

// SSETransport implements MCP over HTTP+SSE (the 2024-11-05 transport): a
// long-lived GET that streams server->client messages, and POSTs to the
// endpoint the server announces for client->server ones.
//
// It exists because that is the only transport that carries per-request
// identity headers end to end in the deployment this was written for.
// ContextForge forwards headers such as X-Forwarded-User-Email to upstream
// servers on its /sse path and strips them on /mcp, so an identity-scoped tool
// reached over /mcp cannot tell who is asking. HTTPTransport — which
// transport_type "sse" used to build, despite the name — speaks /mcp.
//
// The MCP handshake (initialize, notifications/initialized, session id,
// protocol version negotiation) belongs to the SDK's client. This type only
// owns the session's lifecycle and the conversion between the SDK's types and
// nanite's Tool / ToolResult.
type SSETransport struct {
	endpoint   string
	httpClient *http.Client

	// maxResponseBytes is 0 when the package default applies; otherwise
	// it's the tier-derived ceiling. Atomic because SetMaxResponseBytes is
	// reachable concurrently with a request in flight, and the cap is read
	// per-read inside the response body wrapper.
	maxResponseBytes atomic.Int64

	mu   sync.Mutex
	sess *sseSession // nil until the first call connects
}

// sseSession is one live MCP session plus the two things needed to retire it:
// the context that owns the event stream, and a channel closed when the stream
// ends for any reason.
type sseSession struct {
	cs     *sdkmcp.ClientSession
	cancel context.CancelFunc
	dead   chan struct{}
}

// NewSSETransport creates an SSE-based MCP transport for the given endpoint.
// headers, if any, are set on every outbound request — the SSE GET and every
// message POST alike — by a RoundTripper, because the SDK's transports take an
// *http.Client and nothing else. The map is copied so callers may reuse theirs.
func NewSSETransport(endpoint string, headers map[string]string) *SSETransport {
	t := &SSETransport{endpoint: endpoint}

	// No http.Client.Timeout: it bounds the whole exchange including the
	// response body read, and the body here is an event stream that is meant
	// to stay open for the life of the session. The per-phase timeouts below
	// bound the parts that can actually hang, and each call carries its own
	// context deadline.
	base := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
	}
	t.httpClient = &http.Client{
		Transport: &sseRoundTripper{
			base:    base,
			headers: copyHeaders(headers),
			maxEvent: func() int64 {
				return t.effectiveMaxResponseBytes()
			},
		},
	}
	return t
}

func copyHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		out[k] = v
	}
	return out
}

// SetMaxResponseBytes overrides the default 10 MiB cap with a tier-derived
// ceiling. Values <= 0 are ignored so accidental zeroing can't disable the
// cap. Manager calls this after AddServer based on the registered server's
// TrustTier (S4b D2).
func (t *SSETransport) SetMaxResponseBytes(n int) {
	if n > 0 {
		t.maxResponseBytes.Store(int64(n))
	}
}

func (t *SSETransport) effectiveMaxResponseBytes() int64 {
	if v := t.maxResponseBytes.Load(); v > 0 {
		return v
	}
	return int64(maxSSEEventBytes)
}

// acquire returns a live session, connecting or reconnecting as needed.
//
// A session whose stream has ended is retired here rather than on the failure
// of the call that would have used it, so the common case — an idle stream
// dropped by a proxy between two tool calls — costs a reconnect and not a
// failed call.
func (t *SSETransport) acquire(ctx context.Context) (*sseSession, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.sess != nil {
		select {
		case <-t.sess.dead:
			t.sess.close()
			t.sess = nil
		default:
			return t.sess, nil
		}
	}

	sess, err := t.dial(ctx)
	if err != nil {
		return nil, err
	}
	t.sess = sess
	return sess, nil
}

// retireUnlessCallerGaveUp drops the session unless the failure was the
// caller's own cancellation. A user abandoning a turn says nothing about the
// stream's health, and tearing the session down there would make every
// canceled turn cost the next one a reconnect. Our own per-call deadline
// lives on a derived context, so it does not mark ctx done and does retire.
func (t *SSETransport) retireUnlessCallerGaveUp(ctx context.Context, sess *sseSession) {
	if ctx.Err() != nil {
		return
	}
	t.retire(sess)
}

// retire drops sess if it is still the current session. Taking the session
// as an argument means a call that failed on a stale session cannot tear down
// the replacement another goroutine has already established.
func (t *SSETransport) retire(sess *sseSession) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sess == sess {
		t.sess.close()
		t.sess = nil
	}
}

func (s *sseSession) close() {
	if s == nil {
		return
	}
	if s.cs != nil {
		_ = s.cs.Close() // Teardown is best-effort; the session is being discarded either way.
	}
	s.cancel()
}

// dial opens the event stream and completes the MCP handshake.
func (t *SSETransport) dial(ctx context.Context) (*sseSession, error) {
	// The context passed to Connect owns the SSE GET for the whole life of
	// the session, so it cannot be the caller's request-scoped one and it
	// cannot carry the handshake deadline. A watchdog cancels it if the
	// handshake does not finish in time, or if the caller gives up first,
	// and stands down once the session is up.
	sessCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	handshakeDone := make(chan struct{})
	safego.Go(ctx, "mcp.sse.handshake_watchdog", func() {
		timer := time.NewTimer(sseConnectTimeout)
		defer timer.Stop()
		select {
		case <-handshakeDone:
		case <-timer.C:
			cancel()
		case <-ctx.Done():
			cancel()
		}
	})

	client := sdkmcp.NewClient(
		&sdkmcp.Implementation{Name: brand.ID, Version: version.Version},
		&sdkmcp.ClientOptions{
			KeepAlive:                 sseKeepAliveInterval,
			KeepAliveFailureThreshold: sseKeepAliveFailures,
		},
	)
	cs, err := client.Connect(sessCtx, &sdkmcp.SSEClientTransport{
		Endpoint:   t.endpoint,
		HTTPClient: t.httpClient,
	}, nil)
	close(handshakeDone)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("connect sse %s: %w", t.endpoint, err)
	}

	sess := &sseSession{cs: cs, cancel: cancel, dead: make(chan struct{})}
	safego.Go(sessCtx, "mcp.sse.session_wait", func() {
		_ = cs.Wait() // The only thing we need from Wait is that it returned.
		close(sess.dead)
	})
	return sess, nil
}

// Close tears down the live session, if any. Manager calls it via the Close
// interface assertion in RemoveServer.
func (t *SSETransport) Close() error {
	t.mu.Lock()
	sess := t.sess
	t.sess = nil
	t.mu.Unlock()
	sess.close()
	return nil
}

// ListTools returns the server's tools, following pagination.
//
// Unlike CallTool this retries once after a reconnect: tools/list is
// read-only, so re-issuing it against a fresh session cannot double an effect.
// The retry covers the one gap the liveness check in acquire cannot — a stream
// that died between acquiring the session and using it.
//
// Only a call on an established session is retried. A connect that failed is
// returned as-is: retrying it would double what a dead server costs discovery
// at startup, and buy nothing.
func (t *SSETransport) ListTools(ctx context.Context) ([]Tool, error) {
	sess, err := t.acquire(ctx)
	if err != nil {
		return nil, err
	}
	tools, err := t.listToolsOn(ctx, sess)
	if err == nil {
		return tools, nil
	}

	retrySess, retryErr := t.acquire(ctx)
	if retryErr != nil || retrySess == sess {
		return nil, err // the first failure is the informative one
	}
	tools, retryErr = t.listToolsOn(ctx, retrySess)
	if retryErr != nil {
		return nil, err
	}
	return tools, nil
}

// maxSSEToolsPerServer stops a server that paginates without ever setting an
// empty nextCursor from listing forever. It sits above the largest per-tier
// MaxToolsPerServer, so the tier validator — not this — is what a real server
// hits first.
const maxSSEToolsPerServer = 2000

func (t *SSETransport) listToolsOn(ctx context.Context, sess *sseSession) ([]Tool, error) {
	callCtx, cancel := context.WithTimeout(ctx, sseCallTimeout)
	defer cancel()

	var out []Tool
	var cursor string
	for {
		res, err := sess.cs.ListTools(callCtx, &sdkmcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			t.retireUnlessCallerGaveUp(ctx, sess)
			return nil, fmt.Errorf("tools/list: %w", err)
		}
		for _, tool := range res.Tools {
			if tool == nil {
				continue
			}
			converted, err := convertSDKTool(tool)
			if err != nil {
				return nil, fmt.Errorf("tools/list: %w", err)
			}
			out = append(out, converted)
		}
		if res.NextCursor == "" || len(out) >= maxSSEToolsPerServer {
			if res.NextCursor != "" {
				slog.Warn("mcp: sse tools/list truncated at page limit",
					"endpoint", t.endpoint, "tools", len(out))
			}
			return out, nil
		}
		cursor = res.NextCursor
	}
}

// CallTool invokes a tool and converts the result.
//
// A failed call is not retried. A connection error cannot distinguish "the
// request never arrived" from "the reply did not come back", and re-issuing a
// tool call that may already have run is the wrong side to err on. The session
// is retired, so the next call reconnects.
func (t *SSETransport) CallTool(ctx context.Context, name string, arguments map[string]any) (*ToolResult, error) {
	sess, err := t.acquire(ctx)
	if err != nil {
		return nil, err
	}

	callCtx, cancel := context.WithTimeout(ctx, sseCallTimeout)
	defer cancel()

	res, err := sess.cs.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name:      name,
		Arguments: arguments,
	})
	if err != nil {
		t.retireUnlessCallerGaveUp(ctx, sess)
		return nil, fmt.Errorf("tools/call %s: %w", name, err)
	}
	return convertSDKCallResult(res), nil
}

// convertSDKTool maps the SDK's tool definition onto nanite's.
//
// Annotations round-trip through JSON rather than being copied field by
// field: Manager.ToolBehavior reads them as a map keyed by the wire names, and
// a hand-written copy would silently drop any hint the SDK learns about later.
func convertSDKTool(tool *sdkmcp.Tool) (Tool, error) {
	out := Tool{
		Name:        tool.Name,
		Description: tool.Description,
	}

	if tool.InputSchema != nil {
		schema, err := toStringMap(tool.InputSchema)
		if err != nil {
			return Tool{}, fmt.Errorf("tool %q input schema: %w", tool.Name, err)
		}
		out.InputSchema = schema
	}

	if tool.Annotations != nil {
		annotations, err := toStringMap(tool.Annotations)
		if err != nil {
			return Tool{}, fmt.Errorf("tool %q annotations: %w", tool.Name, err)
		}
		out.Annotations = annotations
	}

	return out, nil
}

func toStringMap(v any) (map[string]any, error) {
	if m, ok := v.(map[string]any); ok {
		return m, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// convertSDKCallResult maps the SDK's tool result onto nanite's.
//
// nanite's ToolContent carries text only. Non-text blocks (images, embedded
// resources) are kept as a placeholder of their own type rather than dropped,
// so a result that consisted only of them does not reach the agent as an empty
// success.
func convertSDKCallResult(res *sdkmcp.CallToolResult) *ToolResult {
	out := &ToolResult{IsError: res.IsError}
	for _, c := range res.Content {
		switch content := c.(type) {
		case *sdkmcp.TextContent:
			out.Content = append(out.Content, ToolContent{Type: "text", Text: content.Text})
		case nil:
			continue
		default:
			raw, err := c.MarshalJSON()
			if err != nil {
				out.Content = append(out.Content, ToolContent{
					Type: "text",
					Text: "[unrepresentable content block]",
				})
				continue
			}
			out.Content = append(out.Content, ToolContent{Type: "text", Text: string(raw)})
		}
	}
	// A structured-only result carries its payload nowhere else, and the
	// agent surface is text.
	if len(out.Content) == 0 && res.StructuredContent != nil {
		if raw, err := json.Marshal(res.StructuredContent); err == nil {
			out.Content = append(out.Content, ToolContent{Type: "text", Text: string(raw)})
		}
	}
	return out
}

// sseRoundTripper sets the configured static headers on every outbound
// request and caps the size of a single SSE event.
//
// Headers live here because the SDK's client transports accept an *http.Client
// and nothing else — there is no per-request header hook to use instead, and
// adding one to the SDK types is not ours to do.
type sseRoundTripper struct {
	base     http.RoundTripper
	headers  map[string]string
	maxEvent func() int64
}

func (rt *sseRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// RoundTrip must not modify the request it is given.
	out := req.Clone(req.Context())
	for k, v := range rt.headers {
		out.Header.Set(k, v)
	}

	resp, err := rt.base.RoundTrip(out)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	if isEventStream(resp.Header.Get("Content-Type")) {
		if rt.maxEvent != nil {
			resp.Body = &eventCapReader{inner: resp.Body, max: rt.maxEvent()}
		}
		// See sse_sanitize.go: drops the keepalive events ContextForge sends
		// (which the SDK would hand to the JSON-RPC decoder) and repairs an
		// endpoint URL that names a host:port we did not connect to.
		resp.Body = newSSESanitizeReader(resp.Body, out.URL)
	}
	return resp, nil
}

func isEventStream(contentType string) bool {
	// Content-Type may carry parameters ("text/event-stream; charset=utf-8").
	mediaType, _, _ := strings.Cut(contentType, ";")
	return strings.EqualFold(strings.TrimSpace(mediaType), "text/event-stream")
}

// eventCapReader bounds the bytes an SSE stream may deliver between two event
// boundaries. The whole stream cannot be capped — it is long-lived by design —
// but a single event becomes one JSON-RPC message the SDK buffers whole, so
// that is where an unbounded reply would cost the host its memory.
//
// SSE dispatches an event on a blank line, so the counter resets there.
type eventCapReader struct {
	inner   io.ReadCloser
	max     int64
	since   int64
	lineLen int
}

func (r *eventCapReader) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	for _, b := range p[:n] {
		r.since++
		switch b {
		case '\n':
			if r.lineLen == 0 {
				r.since = 0 // blank line: end of event
			}
			r.lineLen = 0
		case '\r':
			// Part of a CRLF terminator, not line content.
		default:
			r.lineLen++
		}
		if r.since > r.max {
			return n, fmt.Errorf("mcp: sse event exceeded %d bytes", r.max)
		}
	}
	return n, err
}

func (r *eventCapReader) Close() error { return r.inner.Close() }
