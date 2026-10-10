package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/brand"

	condmcp "github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/mcpbridge"
	"github.com/hollis-labs/nanite/internal/selftools"
	"github.com/hollis-labs/nanite/internal/toolclient"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

// selfToolProxy is a toolTransport that forwards self-tool calls to a live
// nanite API server (POST /api/tools/call) instead of dispatching them
// locally.
//
// A `nanite mcp` subprocess spawned for a CLI-launched agent is told the
// live server's address (NANITE_API_URL, planted into the boot dir's
// .mcp.json) and uses this proxy for the `self` transport, so self tools
// dispatch in the running process where their dependencies are live, and
// the subprocess opens no database (CW-20261001-0188). The `dev` transport
// stays local — filesystem tools belong in the subprocess.
type selfToolProxy struct {
	apiURL    string
	sessionID string
	catalog   []condmcp.Tool
	// hidden are the self tools a ScopeStore proxy leaves out of its
	// catalog. A call naming one is answered here and never forwarded.
	hidden map[string]struct{}
	client *http.Client
	// cacheRetrieval is sent with every forwarded call: true when this
	// subprocess exposes fetch_tool_result/search_tool_result, so the harness
	// may cache an over-budget result behind a pointer the agent can follow.
	cacheRetrieval bool
}

// newSelfToolProxy builds a proxy transport. The tool catalog is static —
// the self-tool definitions, which the live server can all dispatch — while
// CallTool forwards over HTTP. scope picks the catalog: ScopeHarness lists
// every self tool and the harness's cache navigation tools, ScopeStore only
// selftools.BareStoreTools.
func newSelfToolProxy(apiURL, sessionID string, scope SelfToolScope) *selfToolProxy {
	p := &selfToolProxy{
		apiURL:    strings.TrimRight(apiURL, "/"),
		sessionID: sessionID,
		// A forwarded self-tool can be a synchronous dispatch (task_execute)
		// that legitimately runs up to the subagent default of 300s. Keep
		// the client timeout well clear of that so a valid long dispatch
		// isn't canceled, while still bounding a genuinely wedged harness.
		// The per-call ctx threaded into the request remains the primary
		// cancellation path.
		client: &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
	all, _ := (&selftools.SelfToolsTransport{}).ListTools(context.Background())
	// The retained operator endpoint does not authenticate an actor binding.
	// Recursive executors cannot use it as plugin authority. Keep the names
	// hidden so an explicit call can receive the typed unavailable refusal.
	p.hidden = map[string]struct{}{"python_run": {}, "workflow_execute_tool_step": {}}
	all = coreProxyCatalog(all)
	if scope == ScopeStore {
		p.catalog = coreProxyCatalog(selftools.BareStoreTools())
		for _, t := range all {
			if !slices.ContainsFunc(p.catalog, func(c condmcp.Tool) bool { return c.Name == t.Name }) {
				p.hidden[t.Name] = struct{}{}
			}
		}
		return p
	}
	p.catalog = all
	// Cache navigation is served by the live harness (it owns the cache), so
	// it is advertised here rather than by the bare local transport.
	for _, def := range []llmtypes.ToolDefinition{toolclient.FetchToolResultMetaTool(), toolclient.SearchToolResultMetaTool()} {
		p.catalog = append(p.catalog, condmcp.Tool{Name: def.Name, Description: def.Description, InputSchema: def.InputSchema})
	}
	return p
}

// ListTools returns the static self-tool catalog.
func (p *selfToolProxy) ListTools(ctx context.Context) ([]condmcp.Tool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return p.catalog, nil
}

// advertises reports whether name is in the proxy's catalog.
func (p *selfToolProxy) advertises(name string) bool {
	return slices.ContainsFunc(p.catalog, func(t condmcp.Tool) bool { return t.Name == name })
}

// HiddenTools names the self tools left out of a ScopeStore catalog, so a
// call that names one gets this proxy's own answer rather than the SDK's
// bare "unknown tool", as on a local bare-store server.
func (p *selfToolProxy) HiddenTools() []string {
	names := make([]string, 0, len(p.hidden))
	for name := range p.hidden {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// CallTool forwards the call to the live API server and decodes the result.
func (p *selfToolProxy) CallTool(ctx context.Context, name string, args map[string]any) (*condmcp.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if unsupportedProxyExecutor(condmcp.Tool{Name: name}) {
		return nil, fmt.Errorf("authority_unavailable: %w", mcpbridge.ErrAuthorityUnavailable)
	}
	if _, hidden := p.hidden[name]; hidden {
		return condmcp.ErrorResult(fmt.Sprintf("%s is not available to this launch: it needs a harness service only chat agents are given", name)), nil
	}
	// Core metadata uses a local static declaration snapshot. It must never
	// expose global plugin inventory/schema through an operator bearer plus a
	// caller-asserted session. Unknown tools cannot fall through to the host.
	if !p.advertises(name) {
		return nil, fmt.Errorf("authority_unavailable: %w", mcpbridge.ErrAuthorityUnavailable)
	}
	if name == "tool_list" || name == "tool_describe" || name == "tool_validate" {
		return p.coreMetadata(ctx, name, args)
	}
	payload := map[string]any{
		"session_id": p.sessionID,
		"name":       name,
		"args":       args,
	}
	if p.cacheRetrieval {
		payload["cache_retrieval"] = true
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("self-tool %q: marshal request: %w", name, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.apiURL+"/api/tools/call", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("self-tool %q: build request: %w", name, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token := os.Getenv(brand.Env("AUTH_TOKEN")); token != "" {
		// This proxy is a local host bridge. Never forward the operator token
		// to a remote destination, URL credentials, or an alternate base path.
		endpoint, parseErr := url.Parse(p.apiURL)
		if parseErr != nil || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
			return nil, fmt.Errorf("self-tool %q: invalid authenticated host endpoint", name)
		}
		ip := net.ParseIP(endpoint.Hostname())
		if ip == nil || !ip.IsLoopback() {
			return nil, fmt.Errorf("self-tool %q: authenticated host endpoint must use a loopback IP", name)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

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
