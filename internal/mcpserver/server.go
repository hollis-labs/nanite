package mcpserver

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/nanite/internal/brand"
	condmcp "github.com/hollis-labs/nanite/internal/mcp"
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
func New(s *store.Store, sessionID string, allowedPaths []string, artifactsRoot, apiURL string) *Server {
	dev := condmcp.NewDevToolsTransport(allowedPaths)
	if artifactsRoot != "" {
		dev = dev.WithArtifactResolver(condmcp.NewStoreArtifactResolver(s), artifactsRoot)
	}

	var self toolTransport
	if apiURL != "" {
		self = newSelfToolProxy(s, apiURL, sessionID)
	} else {
		self = condmcp.NewSelfToolsTransport(s)
	}

	return &Server{
		self:      self,
		dev:       dev,
		sessionID: sessionID,
	}
}

// Run starts the MCP server on stdio (stdin/stdout). Blocks until the
// client disconnects or ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    brand.ID,
		Version: version.Version,
	}, nil)
	s.registerTools(srv)
	slog.Info("mcpserver: starting stdio server", "session_id", s.sessionID)
	return srv.Run(ctx, &mcp.StdioTransport{})
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

func (s *Server) registerTransportTools(srv *mcp.Server, label string, t toolTransport) {
	tools, err := t.ListTools(context.Background())
	if err != nil {
		slog.Error("mcpserver: failed to list tools", "transport", label, "err", err)
		return
	}
	for _, td := range tools {
		tool := buildMCPTool(td)
		name := td.Name
		srv.AddTool(tool, s.makeTransportHandler(t, name))
	}
	slog.Info("mcpserver: registered tools", "transport", label, "count", len(tools))
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
