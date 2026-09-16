package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

// JSONRPCRequest represents a JSON-RPC 2.0 request.
type JSONRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// JSONRPCResponse represents a JSON-RPC 2.0 response.
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError represents a JSON-RPC 2.0 error object.
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Tool represents an MCP tool definition returned by tools/list.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
	// Annotations are the MCP spec's behavior hints — readOnlyHint,
	// destructiveHint, idempotentHint, openWorldHint. They are how a server
	// tells a client that a tool changes something, which is what an approval
	// gate should key off. Inferring it from the tool's name instead works
	// until it doesn't, and the way it fails is letting a write through.
	Annotations map[string]any `json:"annotations,omitempty"`
}

// ToolResult represents the result of a tools/call invocation.
type ToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError"`
}

// ToolContent represents a content block in a tool result.
type ToolContent struct {
	Type string `json:"type"` // text, image, resource
	Text string `json:"text,omitempty"`
}

// maxHTTPResponseBytes caps an HTTP MCP response body so a remote server
// cannot OOM the host with an unbounded JSON-RPC reply (audit
// 2026-04-10-mcp-client-transport finding 02). Per-server overrides are
// deferred to S4b.
const maxHTTPResponseBytes = 10 * 1024 * 1024

// maxHTTPErrorBodyBytes caps the non-OK error body read so a server returning
// a large error payload cannot OOM the host either.
const maxHTTPErrorBodyBytes = 64 * 1024

// HTTPTransport implements an HTTP-based MCP transport.
// It sends JSON-RPC requests to a remote MCP server URL.
type HTTPTransport struct {
	serverURL string
	headers   map[string]string // optional static headers (e.g. Authorization). Read-only after construction.
	client    *http.Client
	nextID    atomic.Int64
	// maxResponseBytes is 0 when the package default applies; otherwise
	// it's the tier-derived ceiling. Atomic because SetMaxResponseBytes is
	// a public method reachable concurrently with call() through the
	// SetMaxResponseBytes interface assertion Manager uses.
	maxResponseBytes atomic.Int64
}

// NewHTTPTransport creates a new HTTP-based MCP transport pointing to the given server URL.
func NewHTTPTransport(serverURL string) *HTTPTransport {
	return &HTTPTransport{
		serverURL: serverURL,
		client: &http.Client{
			Timeout: 60 * time.Second, // longer than stdio's 30s to account for network latency
		},
	}
}

// NewHTTPTransportWithHeaders creates an HTTP-based MCP transport that sends a
// fixed header set on every JSON-RPC request. Used for servers that require
// auth (Bearer token, API key) or routing headers. The headers map is copied
// so callers may reuse or mutate their original after construction.
//
// CW-20260501-0005 sub-ticket 2 (external Tesseract MCP integration scaffold).
func NewHTTPTransportWithHeaders(serverURL string, headers map[string]string) *HTTPTransport {
	t := NewHTTPTransport(serverURL)
	if len(headers) > 0 {
		copy := make(map[string]string, len(headers))
		for k, v := range headers {
			copy[k] = v
		}
		t.headers = copy
	}
	return t
}

// SetMaxResponseBytes overrides the default 10 MiB response cap with a
// tier-derived ceiling. Values ≤ 0 are ignored so accidental zeroing can't
// disable the cap. Manager calls this after AddServer based on the registered
// server's TrustTier (S4b D2).
func (t *HTTPTransport) SetMaxResponseBytes(n int) {
	if n > 0 {
		t.maxResponseBytes.Store(int64(n))
	}
}

// effectiveMaxResponseBytes returns the active cap — the tier override when
// set, the package default otherwise.
func (t *HTTPTransport) effectiveMaxResponseBytes() int64 {
	if v := t.maxResponseBytes.Load(); v > 0 {
		return v
	}
	return int64(maxHTTPResponseBytes)
}

// call sends a JSON-RPC request and parses the response.
func (t *HTTPTransport) call(ctx context.Context, method string, params any) (*JSONRPCResponse, error) {
	id := t.nextID.Add(1)

	rpcReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	payload, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", t.serverURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Declare what this client can actually parse. Go sends no Accept header
	// when none is set, and ContextForge answers that with
	// `406 Not Acceptable: Client must accept application/json` — so without
	// this every tools/list against a CF virtual server fails discovery.
	//
	// Deliberately NOT `application/json, text/event-stream`, which is what
	// the streamable-HTTP spec has clients offer: this transport is a plain
	// JSON-RPC POST client with no SSE decoder, so accepting an event stream
	// would invite a response it cannot read. SSETransport is the one that
	// speaks that. A caller-supplied header still overrides this below.
	req.Header.Set("Accept", "application/json")
	// Apply optional static headers (e.g. Authorization for Tesseract) supplied at
	// construction. Set after Content-Type so callers can override it if
	// needed (rare). The headers map is read-only after construction.
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close() // Response-body close is best-effort cleanup after the request result is read.
	}()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxHTTPErrorBodyBytes))
		return nil, fmt.Errorf("MCP server error %d: %s", resp.StatusCode, string(errBody))
	}

	body := http.MaxBytesReader(nil, resp.Body, t.effectiveMaxResponseBytes())
	var rpcResp JSONRPCResponse
	if err := json.NewDecoder(body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("JSON-RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return &rpcResp, nil
}

// ListTools sends a tools/list request and returns the available tools.
func (t *HTTPTransport) ListTools(ctx context.Context) ([]Tool, error) {
	resp, err := t.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}

	var result struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse tools/list result: %w", err)
	}

	return result.Tools, nil
}

// CallTool sends a tools/call request and returns the result.
func (t *HTTPTransport) CallTool(ctx context.Context, name string, arguments map[string]any) (*ToolResult, error) {
	params := map[string]any{
		"name":      name,
		"arguments": arguments,
	}

	resp, err := t.call(ctx, "tools/call", params)
	if err != nil {
		return nil, fmt.Errorf("tools/call %s: %w", name, err)
	}

	var result ToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse tools/call result: %w", err)
	}

	return &result, nil
}
