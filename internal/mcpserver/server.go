package mcpserver

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	gmcpserver "github.com/hollis-labs/go-mcp/server"

	"github.com/hollis-labs/nanite/internal/brand"
	condmcp "github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/selftools"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/version"
)

// Server is a Nanite MCP server that exposes self-service tools
// (skills, agents, workflows, envelope helpers) and developer filesystem
// tools to external processes like Claude CLI via the MCP stdio protocol.
type Server struct {
	self      toolTransport
	dev       *condmcp.DevToolsTransport
	sessionID string

	// toolAllowlist, when non-nil, restricts registerTools to exactly
	// these tool names — across BOTH the self and dev transports. A nil
	// map (the default, CLI-launched coding agents) means unrestricted:
	// every tool either transport reports is registered. Enforcement
	// happens at registration time (registerTransportTools skips
	// disallowed tools entirely rather than registering-then-hiding),
	// so a disallowed name is never added to the underlying go-mcp/server
	// registry — a CallTool for it fails at the MCP SDK's own dispatch
	// layer ("unknown tool"), not via an application-level check we could
	// get wrong (CW-20260814-0006).
	toolAllowlist map[string]struct{}
}

// New creates a Nanite MCP server backed by the given store. allowedPaths
// controls which filesystem paths dev tools (dev_read, dev_grep, etc.) may
// access — use the same roots the main server is configured with.
//
// artifactsRoot is the configured artifacts storage directory used to
// confine `dev_read(artifact_id=...)` lookups against Context Broker
// stash pointers (SP-20260512-0008 W2C, CW-20260512-0110). Pass "" to
// disable artifact resolution from this stdio server (path-only mode).
//
// apiURL, when non-empty, is the base URL of a live nanite API server. The
// `self` transport then forwards self-tool calls there (POST
// /api/tools/call) so a CLI-launched chat agent dispatches through the
// fully-wired in-process harness instead of this subprocess's bare store.
// Empty keeps the prior local-dispatch behavior. The `dev` filesystem
// tools always run locally regardless.
//
// toolAllowlist, when non-empty, restricts the tools this server ever
// registers with the MCP SDK to exactly these names (across both the
// self and dev transports) — see the Server.toolAllowlist field comment.
// Pass nil/empty for the default, unrestricted catalog (CLI-launched
// coding agents; CW-20260814-0006).
func New(s *store.Store, sessionID string, allowedPaths []string, artifactsRoot, apiURL string, toolAllowlist []string) *Server {
	dev := condmcp.NewDevToolsTransport(allowedPaths)
	if artifactsRoot != "" {
		dev = dev.WithArtifactResolver(condmcp.NewStoreArtifactResolver(s), artifactsRoot)
	}

	var self toolTransport
	if apiURL != "" {
		self = newSelfToolProxy(s, apiURL, sessionID)
	} else {
		self = selftools.NewSelfToolsTransport(s)
	}

	return &Server{
		self:          self,
		dev:           dev,
		sessionID:     sessionID,
		toolAllowlist: buildToolAllowlist(toolAllowlist),
	}
}

// buildToolAllowlist normalizes a raw tool-name list into a lookup set,
// trimming whitespace and dropping empties. Returns nil (unrestricted)
// when the input carries no usable names.
func buildToolAllowlist(names []string) map[string]struct{} {
	if len(names) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		set[name] = struct{}{}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// Run starts the MCP server on stdio (stdin/stdout). Blocks until the
// client disconnects or ctx is canceled.
func (s *Server) Run(ctx context.Context) error {
	srv := s.buildMCPServer()
	slog.Info("mcpserver: starting stdio server", "session_id", s.sessionID)
	return srv.Run(ctx)
}

// buildMCPServer assembles the underlying go-mcp/server server with tools
// registered per registerTools, without binding it to any transport.
// Factored out of Run so tests can connect it over an in-memory transport
// (via SDKServer().Connect / mcp.NewInMemoryTransports) and drive real
// ListTools/CallTool requests instead of only inspecting what got
// registered.
func (s *Server) buildMCPServer() *gmcpserver.Server {
	srv := gmcpserver.NewServer(brand.ID, version.Version)
	s.registerTools(srv)
	return srv
}

// registerTools adds self-service and developer tool definitions to the MCP server.
func (s *Server) registerTools(srv *gmcpserver.Server) {
	s.registerTransportTools(srv, "self", s.self)
	s.registerTransportTools(srv, "dev", s.dev)
}

type toolTransport interface {
	ListTools(ctx context.Context) ([]condmcp.Tool, error)
	CallTool(ctx context.Context, name string, args map[string]any) (*condmcp.ToolResult, error)
}

// toolAllowed reports whether name may be registered with the MCP SDK.
// A nil allowlist (the default) means unrestricted.
func (s *Server) toolAllowed(name string) bool {
	if s.toolAllowlist == nil {
		return true
	}
	_, ok := s.toolAllowlist[name]
	return ok
}

func (s *Server) registerTransportTools(srv *gmcpserver.Server, label string, t toolTransport) {
	tools, err := t.ListTools(context.Background())
	if err != nil {
		slog.Error("mcpserver: failed to list tools", "transport", label, "err", err)
		return
	}
	registered := 0
	skipped := 0
	for _, td := range tools {
		if !s.toolAllowed(td.Name) {
			// Deliberately never reaches srv.RegisterTool: the MCP SDK's own
			// callTool rejects a request for a name it never registered
			// ("unknown tool %q") before it can reach this transport's
			// CallTool. Skipping registration IS the dispatch gate, not
			// just a discovery-list filter (CW-20260814-0006).
			skipped++
			continue
		}
		srv.RegisterTool(buildTool(td, s.makeTransportHandler(t, td.Name)))
		registered++
	}
	slog.Info("mcpserver: registered tools", "transport", label, "count", registered, "skipped", skipped)
}

// buildTool converts a Nanite Tool definition, and its dispatch handler,
// into a go-mcp/server registration. Annotations come from the per-name
// table in annotations.go — see its package doc for why an unaudited name
// defaults to "assume it's dangerous" rather than "assume it's safe".
func buildTool(t condmcp.Tool, handler gmcpserver.ToolHandler) gmcpserver.Tool {
	schema := t.InputSchema
	if schema == nil {
		schema = gmcpserver.EmptyObjectSchema()
	}
	a := annotationsFor(t.Name)
	return gmcpserver.Tool{
		Name:        t.Name,
		Description: t.Description,
		InputSchema: schema,
		Handler:     handler,

		ReadOnlyHint:    a.ReadOnlyHint,
		DestructiveHint: a.DestructiveHint,
		IdempotentHint:  a.IdempotentHint,
		OpenWorldHint:   a.OpenWorldHint,
	}
}

// makeHandler returns a tool handler that delegates to the self transport.
func (s *Server) makeHandler(name string) gmcpserver.ToolHandler {
	return s.makeTransportHandler(s.self, name)
}

// makeTransportHandler returns a tool handler that delegates to the given
// transport. go-mcp/server's ToolHandler contract already decodes the raw
// wire arguments into args before calling this (a malformed-JSON call never
// reaches here at all — it's rejected as a protocol-level JSON-RPC error by
// go-mcp/server itself, not folded into a tool result the way the prior
// hand-rolled SDK wiring did), so this only has to bridge condmcp's
// Tool/ToolResult shape onto go-mcp/server's (any, error) contract.
//
// result.IsError (set via errorResult() in every self/dev tool handler)
// must propagate onto the wire result — otherwise a caller reading
// CallToolResult.IsError (e.g. a real MCP client, not just this repo's own
// tests which read the text body) can never distinguish a handler-reported
// failure from a success. Found via CW-20260813-0011's end-to-end
// verification: workflow_verify_step's pass/fail is exactly this flag, so a
// caller silently seeing IsError=false on every failure defeats the tool's
// purpose. Returning an error here is how that happens: go-mcp/server's own
// ToolHandler contract reports any returned error as IsError=true content,
// so converting a result.IsError=true response into a Go error reproduces
// the same wire shape without this package needing its own
// CallToolResult-building or SetError/GetError bookkeeping (unused anywhere
// in this codebase, and not offered by go-mcp/server's simplified surface).
func (s *Server) makeTransportHandler(t toolTransport, name string) gmcpserver.ToolHandler {
	return func(ctx context.Context, args map[string]any) (any, error) {
		result, err := t.CallTool(ctx, name, args)
		if err != nil {
			return nil, err
		}
		text := convertEnvelopeMarkers(extractText(result))
		if result != nil && result.IsError {
			return nil, errors.New(text)
		}
		return text, nil
	}
}
