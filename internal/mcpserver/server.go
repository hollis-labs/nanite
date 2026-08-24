package mcpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

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
	// so a disallowed name is never added to the underlying *mcp.Server
	// — a CallTool for it fails at the MCP SDK's own dispatch layer
	// ("unknown tool"), not via an application-level check we could get
	// wrong (CW-20260814-0006).
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
	return srv.Run(ctx, &mcp.StdioTransport{})
}

// buildMCPServer assembles the underlying MCP SDK server with tools
// registered per registerTools, without binding it to any transport.
// Factored out of Run so tests can connect it over an in-memory
// transport (mcp.NewInMemoryTransports) and drive real ListTools/CallTool
// requests instead of only inspecting what got registered.
func (s *Server) buildMCPServer() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    brand.ID,
		Version: version.Version,
	}, nil)
	s.registerTools(srv)
	return srv
}

// registerTools adds self-service and developer tool definitions to the MCP server.
func (s *Server) registerTools(srv *mcp.Server) {
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

func (s *Server) registerTransportTools(srv *mcp.Server, label string, t toolTransport) {
	tools, err := t.ListTools(context.Background())
	if err != nil {
		slog.Error("mcpserver: failed to list tools", "transport", label, "err", err)
		return
	}
	registered := 0
	skipped := 0
	for _, td := range tools {
		if !s.toolAllowed(td.Name) {
			// Deliberately never reaches srv.AddTool: the MCP SDK's own
			// callTool rejects a request for a name it never registered
			// ("unknown tool %q") before it can reach this transport's
			// CallTool. Skipping registration IS the dispatch gate, not
			// just a discovery-list filter (CW-20260814-0006).
			skipped++
			continue
		}
		tool := buildMCPTool(td)
		name := td.Name
		srv.AddTool(tool, s.makeTransportHandler(t, name))
		registered++
	}
	slog.Info("mcpserver: registered tools", "transport", label, "count", registered, "skipped", skipped)
}

// buildMCPTool converts a Nanite Tool definition to an official SDK Tool.
func buildMCPTool(t condmcp.Tool) *mcp.Tool {
	tool := &mcp.Tool{
		Name:        t.Name,
		Description: t.Description,
	}
	raw := mustMarshalSchema(t.InputSchema)
	var schema *jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		slog.Warn("mcpserver: failed to unmarshal tool input schema, using fallback", "tool", t.Name, "err", err)
		if fallbackErr := json.Unmarshal([]byte(`{"type":"object","properties":{}}`), &schema); fallbackErr != nil {
			slog.Error("mcpserver: failed to unmarshal fallback schema", "tool", t.Name, "err", fallbackErr)
			schema = &jsonschema.Schema{}
		}
	}
	if schema == nil {
		schema = &jsonschema.Schema{}
	}
	tool.InputSchema = schema
	return tool
}

// makeHandler returns a tool handler that delegates to the self transport.
func (s *Server) makeHandler(name string) mcp.ToolHandler {
	return s.makeTransportHandler(s.self, name)
}

// makeTransportHandler returns a tool handler that delegates to the given transport.
func (s *Server) makeTransportHandler(t toolTransport, name string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := map[string]any{}
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return newErrorResult(err), nil
			}
		}
		result, err := t.CallTool(ctx, name, args)
		if err != nil {
			return newErrorResult(err), nil
		}
		text := extractText(result)
		text = convertEnvelopeMarkers(text)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
			// result.IsError (set via errorResult() in every self/dev tool
			// handler) must propagate onto the wire result — otherwise a
			// caller reading CallToolResult.IsError (e.g. a real MCP
			// client, not just this repo's own tests which read the text
			// body) can never distinguish a handler-reported failure from
			// a success. Found via CW-20260813-0011's end-to-end
			// verification: workflow_verify_step's pass/fail is exactly
			// this flag, so a caller silently seeing IsError=false on
			// every failure defeats the tool's purpose.
			IsError: result != nil && result.IsError,
		}, nil
	}
}

// newErrorResult builds a CallToolResult flagged as an error, carrying
// the given error's message as its content. The error value is also
// preserved on the result via SetError so server-side middleware can
// observe the original type/unwrap chain via GetError(). Matches the
// prior mark3labs NewToolResultError wire semantics (isError:true +
// text content).
func newErrorResult(err error) *mcp.CallToolResult {
	r := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}
	r.SetError(err)
	return r
}
