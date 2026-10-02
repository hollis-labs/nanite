package pluginapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ToolCall uses Nanite's loopback tool-call wire shape. SessionID scopes a
// call; it is not a credential. This surface can execute tools as the desktop
// user. It is not the read-only query surface or a plugin permission grant.
type ToolCall struct {
	SessionID string         `json:"session_id"`
	Name      string         `json:"name"`
	Args      map[string]any `json:"args"`
}

// ToolResult preserves MCP content blocks without depending on Nanite internals.
// IsError represents a tool error returned by a successful transport call.
type ToolResult struct {
	Content []json.RawMessage `json:"content"`
	IsError bool              `json:"isError"`
}

// Client calls an explicitly configured local Nanite API. It never resolves a
// server from environment variables or reads credentials. All redirects are
// refused. A caller supplies context deadlines for individual tool calls.
type Client struct {
	endpoint string
	http     *http.Client
}

// NewClient accepts a literal loopback IP with an HTTP(S) scheme and optional
// port, not a DNS name, credentials, path, query or fragment. An optional
// transport supports local TLS/test setup; nil uses the standard transport with
// proxies disabled so local session content never reaches an environment proxy.
func NewClient(baseURL string, transport http.RoundTripper) (*Client, error) {
	u, err := loopbackHostURL(baseURL)
	if err != nil {
		return nil, err
	}
	if transport == nil {
		transport = &http.Transport{
			DialContext:       (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2: true, MaxIdleConns: 100, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
		}
	}
	return &Client{endpoint: strings.TrimSuffix(u.String(), "/") + "/api/tools/call", http: &http.Client{
		Transport: transport, Timeout: 10 * time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("pluginapi: host redirects are refused") },
	}}, nil
}

const maxToolResponseBytes = 4 << 20

// CallTool forwards one request without retries. A retry after an uncertain
// response could repeat a destructive action. Non-200 responses are transport
// errors; a 200 response with isError is returned for the caller to inspect.
func (c *Client) CallTool(ctx context.Context, call ToolCall) (ToolResult, error) {
	if strings.TrimSpace(call.Name) == "" {
		return ToolResult{}, fmt.Errorf("pluginapi: tool name is required")
	}
	raw, err := json.Marshal(call)
	if err != nil {
		return ToolResult{}, fmt.Errorf("pluginapi: encode tool call: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(raw))
	if err != nil {
		return ToolResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return ToolResult{}, err
	}
	defer resp.Body.Close()
	raw, err = io.ReadAll(io.LimitReader(resp.Body, maxToolResponseBytes+1))
	if err != nil {
		return ToolResult{}, err
	}
	if len(raw) > maxToolResponseBytes {
		return ToolResult{}, fmt.Errorf("pluginapi: tool response exceeds %d bytes", maxToolResponseBytes)
	}
	if resp.StatusCode != http.StatusOK {
		// Do not copy response bodies into errors: they may contain secrets.
		return ToolResult{}, fmt.Errorf("pluginapi: host returned HTTP %d", resp.StatusCode)
	}
	var result ToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return ToolResult{}, fmt.Errorf("pluginapi: invalid tool response: %w", err)
	}
	if result.Content == nil {
		return ToolResult{}, fmt.Errorf("pluginapi: tool response is missing content")
	}
	return result, nil
}

func loopbackHostURL(baseURL string) (*url.URL, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("pluginapi: invalid host URL")
	}
	ip := net.ParseIP(u.Hostname())
	if (u.Scheme != "http" && u.Scheme != "https") || ip == nil || !ip.IsLoopback() || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return nil, fmt.Errorf("pluginapi: host URL must name a literal loopback HTTP(S) address")
	}
	return u, nil
}
