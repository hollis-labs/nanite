package mcpclient

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/libs/agent/agentdir"
	"github.com/hollis-labs/libs/agent/core/tooluse"
	mcp "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/client"
	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/libs/security/secrets"
	"github.com/hollis-labs/nanite/internal/elicitation"
	"github.com/hollis-labs/nanite/internal/processenv"
	"github.com/hollis-labs/nanite/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxResponseBytes = 1 << 20
const discoveryConcurrency = 4

type Client struct {
	mu        sync.Mutex
	pool      *mcp.Pool
	servers   map[string]*registration
	sequence  uint64
	byKey     sync.Map
	discovery chan struct{}
	dir       string
	resolver  *secrets.Resolver
	scope     *redact.Scope
	logger    *slog.Logger
}

type registration struct {
	name, key        string
	callSlot         chan struct{}
	question         callQuestion
	legacySSE        bool
	digest           [32]byte
	users            int
	current          bool
	generation       atomic.Uint64
	listing          chan struct{}
	listed           bool
	listedGeneration uint64
	specs            []*sdk.Tool
}

type Lease struct {
	client  *Client
	servers []*registration
	once    sync.Once
}

func New(dir string, resolver *secrets.Resolver, scope *redact.Scope, logger *slog.Logger) *Client {
	if resolver == nil {
		resolver = secrets.New()
	}
	c := &Client{dir: dir, resolver: resolver, scope: scope, logger: logger, servers: make(map[string]*registration),
		discovery: make(chan struct{}, discoveryConcurrency)}
	c.pool = mcp.NewPool(mcp.WithIdentity("nanite", "0.5.0"), mcp.WithRetries(1),
		mcp.WithCommandEnv(commandEnv), mcp.WithMaxResponseBytes(maxResponseBytes), mcp.WithHTTPClient(httpClient),
		mcp.WithDefaultCallTimeouts(map[string]time.Duration{"stdio": 60 * time.Second, "http": 60 * time.Second, "sse": 60 * time.Second}),
		mcp.WithClientOptions(func(key string, options *sdk.ClientOptions) {
			options.ElicitationHandler = c.elicit(key)
			options.Capabilities = &sdk.ClientCapabilities{Elicitation: &sdk.ElicitationCapabilities{Form: &sdk.FormElicitationCapabilities{}}}

		}),
		mcp.WithToolListChangedHandler(func(_ context.Context, key string) {
			if value, ok := c.byKey.Load(key); ok {
				value.(*registration).generation.Add(1)
			}
		}))
	return c
}

// Reconcile issues a turn lease over the authored server set. A replaced
// connection stays alive until turns still using its configuration finish.
func (c *Client) Reconcile(ctx context.Context, cfg agentdir.MCPConfig) (*Lease, []string) {
	type prepared struct {
		config mcp.ServerConfig
		digest [32]byte
	}
	next := make(map[string]prepared, len(cfg.Servers))
	var warnings []string
	for _, name := range slices.Sorted(maps.Keys(cfg.Servers)) {
		resolved, err := c.resolve(ctx, cfg.Servers[name])
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("MCP server %q configuration: %v", name, err))
			continue
		}
		raw, err := json.Marshal(resolved)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("MCP server %q configuration could not be encoded", name))
			continue
		}
		next[name] = prepared{resolved, sha256.Sum256(raw)}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for name, reg := range c.servers {
		if incoming, ok := next[name]; !ok || incoming.digest != reg.digest {
			delete(c.servers, name)
			reg.current = false
			c.releaseIfInactive(reg)
		}
	}
	lease := &Lease{client: c}
	for _, name := range slices.Sorted(maps.Keys(next)) {
		entry := next[name]
		reg := c.servers[name]
		if reg == nil {
			c.sequence++
			key := fmt.Sprintf("%s#%d", name, c.sequence)
			if err := c.pool.Register(key, entry.config); err != nil {
				warnings = append(warnings, fmt.Sprintf("MCP server %q registration failed: %v", name, err))
				continue
			}
			reg = &registration{name: name, key: key, digest: entry.digest, legacySSE: entry.config.Transport == "sse", current: true, listing: make(chan struct{}, 1), callSlot: make(chan struct{}, 1)}
			c.byKey.Store(key, reg)
			c.servers[name] = reg
		}
		reg.users++
		lease.servers = append(lease.servers, reg)
	}
	return lease, warnings
}

func (c *Client) releaseIfInactive(reg *registration) {
	if !reg.current && reg.users == 0 {
		c.byKey.Delete(reg.key)
		if err := c.pool.Deregister(reg.key); err != nil {
			c.logger.Warn("MCP server close failed", "server", reg.name)
		}
	}
}

func (l *Lease) Close() {
	l.once.Do(func() {
		c := l.client
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, reg := range l.servers {
			reg.users--
			c.releaseIfInactive(reg)
		}
	})
}

func (c *Client) Close() error { return c.pool.Close() }

func (l *Lease) Tools(ctx context.Context) ([]tools.Tool, []string) {
	var out []tools.Tool
	var warnings []string
	seen := make(map[string]bool)
	type discoveryResult struct {
		specs []*sdk.Tool
		err   error
	}
	discovered := make([]discoveryResult, len(l.servers))
	tooluse.RunBounded(ctx, len(l.servers), discoveryConcurrency, func(i int) {
		listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		discovered[i].specs, discovered[i].err = l.cachedList(listCtx, l.servers[i])
	}, func(i int) { discovered[i].err = ctx.Err() })
	for i, reg := range l.servers {
		specs, err := discovered[i].specs, discovered[i].err
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("MCP server %q tools unavailable: %v", reg.name, err))
			continue
		}
		for _, spec := range specs {
			tool, err := l.tool(reg, spec)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("MCP server %q: %v", reg.name, err))
				continue
			}
			if seen[tool.Name] {
				warnings = append(warnings, fmt.Sprintf("MCP tool %q has a duplicate model name", tool.Name))
				continue
			}
			seen[tool.Name] = true
			out = append(out, tool)
		}
	}
	if len(out) > 100 {
		l.client.logger.WarnContext(ctx, "MCP tool definitions may crowd the context", "tool_count", len(out))
	}
	return out, warnings
}

func (l *Lease) cachedList(ctx context.Context, reg *registration) ([]*sdk.Tool, error) {
	select {
	case reg.listing <- struct{}{}:
		defer func() { <-reg.listing }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	version := reg.generation.Load()
	if reg.listed && reg.listedGeneration == version {
		return reg.specs, nil
	}
	select {
	case l.client.discovery <- struct{}{}:
		defer func() { <-l.client.discovery }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	specs, err := l.list(ctx, reg)
	if err != nil {
		return nil, err
	}
	reg.specs, reg.listedGeneration, reg.listed = specs, version, true
	return specs, nil
}

func (l *Lease) list(ctx context.Context, reg *registration) ([]*sdk.Tool, error) {
	page, err := l.client.pool.ListTools(reg.transportContext(ctx), reg.key)
	if err != nil {
		return nil, err
	}
	out := page.Tools
	if page.NextCursor == "" {
		return out, nil
	}
	conn, err := l.client.pool.Get(reg.key)
	if err != nil {
		return nil, err
	}
	session := conn.SDKSession()
	if session == nil {
		return nil, fmt.Errorf("MCP session closed during discovery")
	}
	for tool, err := range session.Tools(ctx, &sdk.ListToolsParams{Cursor: page.NextCursor}) {
		if err != nil {
			return nil, err
		}
		out = append(out, tool)
	}
	return out, nil
}

func (l *Lease) tool(reg *registration, spec *sdk.Tool) (tools.Tool, error) {
	if spec == nil || spec.Name == "" {
		return tools.Tool{}, fmt.Errorf("tool name is missing")
	}
	schema, err := json.Marshal(spec.InputSchema)
	if err != nil || len(schema) == 0 || schema[0] != '{' {
		return tools.Tool{}, fmt.Errorf("invalid input schema for %q", spec.Name)
	}
	// Validating a call against an unsafe schema (one that refers back to
	// itself in place, is too large, or makes validation do too much work)
	// could end the process, so such a tool is left out. Other schema
	// problems still surface on the call, as before.
	if _, err := elicitation.Resolve(schema); errors.Is(err, elicitation.ErrUnsafeSchema) {
		return tools.Tool{}, fmt.Errorf("tool %q left out: %w", spec.Name, err)
	}
	readonly := spec.Annotations != nil && spec.Annotations.ReadOnlyHint
	destructive := !readonly && (spec.Annotations == nil || spec.Annotations.DestructiveHint == nil || *spec.Annotations.DestructiveHint)
	return tools.Tool{
		Name: "mcp__" + modelName(reg.name) + "__" + modelName(spec.Name), Description: spec.Description, InputSchema: schema, ReadOnly: readonly, Destructive: destructive,
		Handler: func(ctx context.Context, call tools.Call) (tools.Result, error) {
			var args map[string]any
			if err := json.Unmarshal(call.Input, &args); err != nil {
				return tools.Result{}, err
			}
			release, err := reg.bind(ctx)
			if err != nil {
				return tools.Result{}, err
			}
			defer release()
			result, _, err := l.client.pool.CallTool(reg.transportContext(ctx), reg.key, spec.Name, args, mcp.WithCallRetry(mcp.RetryIfUnsent))
			if err != nil {
				if isUnknownTool(err) {
					reg.generation.Add(1)
				}
				return tools.Result{}, err
			}
			return toolResult(result)
		},
	}, nil
}

func isUnknownTool(err error) bool {
	var wire *jsonrpc.Error
	if !errors.As(err, &wire) {
		return false
	}
	message := strings.ToLower(wire.Message)
	return strings.Contains(message, "unknown tool") || strings.Contains(message, "tool not found") ||
		(wire.Code == jsonrpc.CodeInvalidParams && strings.HasPrefix(message, "tool ") && strings.HasSuffix(message, " not found"))
}

func modelName(name string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, name)
}

func toolResult(result *sdk.CallToolResult) (tools.Result, error) {
	if result == nil {
		return tools.Result{}, fmt.Errorf("empty MCP result")
	}
	var parts []string
	for _, content := range result.Content {
		if text, ok := content.(*sdk.TextContent); ok {
			parts = append(parts, text.Text)
			continue
		}
		raw, err := json.Marshal(content)
		if err != nil {
			return tools.Result{}, err
		}
		parts = append(parts, string(raw))
	}
	if len(parts) == 0 && result.StructuredContent != nil {
		raw, err := json.Marshal(result.StructuredContent)
		if err != nil {
			return tools.Result{}, err
		}
		parts = append(parts, string(raw))
	}
	text := strings.Join(parts, "\n")
	if len(text) > maxResponseBytes {
		text = strings.ToValidUTF8(text[:maxResponseBytes], "") + "\n[MCP output truncated]"
	}
	return tools.Result{Text: text, IsError: result.IsError}, nil
}

func (c *Client) resolve(ctx context.Context, input agentdir.MCPServer) (mcp.ServerConfig, error) {
	out := mcp.ServerConfig{Transport: input.Type, Command: input.Command, Args: input.Args, URL: input.URL}
	if endpoint, err := url.Parse(out.URL); err == nil && endpoint.RawQuery != "" {
		for key, values := range endpoint.Query() {
			for _, value := range values {
				c.scope.Add(key, value)
			}
		}
	}

	if out.Transport == "stdio" && strings.ContainsRune(out.Command, '/') && !filepath.IsAbs(out.Command) {
		out.Command = filepath.Join(c.dir, out.Command)
	}
	if out.Transport == "stdio" {
		out.Env = processenv.Build(nil)
		for key, value := range input.Env {
			expanded, err := c.expand(ctx, key, value)
			if err != nil {
				return mcp.ServerConfig{}, err
			}
			out.Env[key] = expanded
		}
	}
	if len(input.Headers) > 0 {
		out.Headers = make(map[string]string, len(input.Headers))
		for key, value := range input.Headers {
			expanded, err := c.expand(ctx, key, value)
			if err != nil {
				return mcp.ServerConfig{}, err
			}
			c.scope.Add(key, expanded)
			out.Headers[key] = expanded
		}
	}
	return out, nil
}

func (c *Client) expand(ctx context.Context, key, value string) (string, error) {
	if strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}") {
		name := value[2 : len(value)-1]
		found, ok := os.LookupEnv(name)
		if !ok {
			return "", fmt.Errorf("variable %q for %q is unset", name, key)
		}
		c.scope.Add(key, found)
		return found, nil
	}
	if secrets.IsRef(value) {
		resolved, err := c.resolver.Resolve(ctx, value)
		if err != nil {
			return "", err
		}
		text := resolved.Secret.Reveal()
		c.scope.Add(key, text)
		return text, nil
	}
	return value, nil
}

func commandEnv(cfg mcp.ServerConfig) ([]string, error) {
	if !strings.ContainsRune(cfg.Command, '/') && cfg.Env["PATH"] == "" {
		return nil, fmt.Errorf("MCP stdio command requires PATH")
	}
	return processenv.List(cfg.Env), nil
}
