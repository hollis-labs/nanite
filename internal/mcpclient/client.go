package mcpclient

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/libs/agent/agentdir"
	mcp "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/client"
	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/libs/security/secrets"
	"github.com/hollis-labs/nanite/internal/tools"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxResponseBytes = 1 << 20

type Client struct {
	mu       sync.Mutex
	pool     *mcp.Pool
	servers  map[string]*registration
	sequence uint64
	dir      string
	resolver *secrets.Resolver
	scope    *redact.Scope
	logger   *slog.Logger
}

type registration struct {
	name, key string
	digest    [32]byte
	users     int
	current   bool
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
	return &Client{
		dir: dir, resolver: resolver, scope: scope, logger: logger, servers: make(map[string]*registration),
		pool: mcp.NewPool(mcp.WithIdentity("nanite", "0.5.0"), mcp.WithRetries(1),
			mcp.WithCommandEnv(commandEnv), mcp.WithMaxResponseBytes(maxResponseBytes),
			mcp.WithDefaultCallTimeouts(map[string]time.Duration{"stdio": 60 * time.Second, "http": 60 * time.Second, "sse": 60 * time.Second})),
	}
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
			reg = &registration{name: name, key: key, digest: entry.digest, current: true}
			c.servers[name] = reg
		}
		reg.users++
		lease.servers = append(lease.servers, reg)
	}
	return lease, warnings
}

func (c *Client) releaseIfInactive(reg *registration) {
	if !reg.current && reg.users == 0 {
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
	for _, reg := range l.servers {
		listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		specs, err := l.list(listCtx, reg)
		cancel()
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
	return out, warnings
}

func (l *Lease) list(ctx context.Context, reg *registration) ([]*sdk.Tool, error) {
	page, err := l.client.pool.ListTools(ctx, reg.key)
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
	readonly := spec.Annotations != nil && spec.Annotations.ReadOnlyHint
	destructive := !readonly && (spec.Annotations == nil || spec.Annotations.DestructiveHint == nil || *spec.Annotations.DestructiveHint)
	return tools.Tool{
		Name: "mcp__" + modelName(reg.name) + "__" + modelName(spec.Name), Description: spec.Description, InputSchema: schema, ReadOnly: readonly, Destructive: destructive,
		Handler: func(ctx context.Context, call tools.Call) (tools.Result, error) {
			var args map[string]any
			if err := json.Unmarshal(call.Input, &args); err != nil {
				return tools.Result{}, err
			}
			result, _, err := l.client.pool.CallTool(ctx, reg.key, spec.Name, args, mcp.WithCallRetry(mcp.RetryIfUnsent))
			if err != nil {
				return tools.Result{}, err
			}
			return toolResult(result)
		},
	}, nil
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
		out.Env = make(map[string]string)
		for _, key := range []string{"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "LC_ALL", "LC_CTYPE", "TERM", "TMPDIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
			if val, ok := os.LookupEnv(key); ok {
				out.Env[key] = val
			}
		}
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
	env := make([]string, 0, len(cfg.Env))
	for _, key := range slices.Sorted(maps.Keys(cfg.Env)) {
		env = append(env, key+"="+cfg.Env[key])
	}
	return env, nil
}
