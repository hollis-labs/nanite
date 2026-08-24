package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	condmcp "github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/selftools"
	"github.com/hollis-labs/nanite/internal/store"
)

// selfToolProxy is a toolTransport that forwards self-tool calls to a live
// nanite API server (POST /api/tools/call) instead of dispatching them
// locally.
//
// A `nanite mcp` subprocess spawned for a CLI-launched chat agent runs
// against a bare store with none of the harness services wired. When the
// subprocess is told the live server's address (NANITE_API_URL, planted
// into the boot dir's .mcp.json), it uses this proxy for the `self`
// transport so todo/plan/panel/messaging/subagent tools dispatch in the
// running process where their dependencies are live. The `dev` transport
// stays local — filesystem tools belong in the subprocess.
type selfToolProxy struct {
	apiURL    string
	sessionID string
	catalog   []condmcp.Tool
	client    *http.Client
}

// newSelfToolProxy builds a proxy transport. The tool catalog is taken from
// a local SelfToolsTransport — selfToolDefinitions() is static, so the
// advertised surface matches what the live server can dispatch — while
// CallTool forwards over HTTP.
func newSelfToolProxy(s *store.Store, apiURL, sessionID string) *selfToolProxy {
	catalog, _ := selftools.NewSelfToolsTransport(s).ListTools(context.Background())
	return &selfToolProxy{
		apiURL:    strings.TrimRight(apiURL, "/"),
		sessionID: sessionID,
		catalog:   catalog,
		// A forwarded self-tool can be a synchronous dispatch (task_execute)
		// that legitimately runs up to the subagent default of 300s. Keep
		// the client timeout well clear of that so a valid long dispatch
		// isn't cancelled, while still bounding a genuinely wedged harness.
		// The per-call ctx threaded into the request remains the primary
		// cancellation path.
		client: &http.Client{Timeout: 10 * time.Minute},
	}
}

// ListTools returns the static self-tool catalog.
func (p *selfToolProxy) ListTools(_ context.Context) ([]condmcp.Tool, error) {
	return p.catalog, nil
}

// CallTool forwards the call to the live API server and decodes the result.
func (p *selfToolProxy) CallTool(ctx context.Context, name string, args map[string]any) (*condmcp.ToolResult, error) {
	body, err := json.Marshal(map[string]any{
		"session_id": p.sessionID,
		"name":       name,
		"args":       args,
	})
	if err != nil {
		return nil, fmt.Errorf("self-tool %q: marshal request: %w", name, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.apiURL+"/api/tools/call", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("self-tool %q: build request: %w", name, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("self-tool %q: forward to harness: %w", name, err)
	}
	defer func() {
		_ = resp.Body.Close() // Response-body close is best-effort cleanup after the request result is read.
	}()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("self-tool %q: read response: %w", name, err)
	}

	if resp.StatusCode != http.StatusOK {
		// The endpoint returns {"error": "..."} for transport failures.
		var errBody struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &errBody) == nil && errBody.Error != "" {
			return nil, fmt.Errorf("self-tool %q: harness rejected: %s", name, errBody.Error)
		}
		return nil, fmt.Errorf("self-tool %q: harness returned %s", name, resp.Status)
	}

	var result condmcp.ToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("self-tool %q: decode result: %w", name, err)
	}
	return &result, nil
}
