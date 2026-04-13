package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/nanite/internal/brand"
	condmcp "github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/version"
)

// Server is a Nanite MCP server that exposes self-service tools
// (skills, agents, workflows, envelope helpers) to external processes
// like Claude CLI via the MCP stdio protocol.
type Server struct {
	self      *condmcp.SelfToolsTransport
	sessionID string
}

// New creates a Nanite MCP server backed by the given store.
func New(s *store.Store, sessionID string) *Server {
	return &Server{
		self:      condmcp.NewSelfToolsTransport(s),
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

// registerTools adds all self-service tool definitions to the MCP server.
func (s *Server) registerTools(srv *mcp.Server) {
	tools, err := s.self.ListTools(context.Background())
	if err != nil {
		slog.Error("mcpserver: failed to list tools", "err", err)
		return
	}

	for _, t := range tools {
		tool := buildMCPTool(t)
		name := t.Name
		srv.AddTool(tool, s.makeHandler(name))
	}
	slog.Info("mcpserver: registered tools", "count", len(tools))
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
		// Fallback: empty object schema.
		_ = json.Unmarshal([]byte(`{"type":"object","properties":{}}`), &schema)
	}
	tool.InputSchema = schema
	return tool
}

// makeHandler returns a tool handler that delegates to SelfToolsTransport.
func (s *Server) makeHandler(name string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := map[string]any{}
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return newErrorResult(err.Error()), nil
			}
		}
		result, err := s.self.CallTool(ctx, name, args)
		if err != nil {
			return newErrorResult(err.Error()), nil
		}
		text := extractText(result)
		text = convertEnvelopeMarkers(text)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, nil
	}
}

// newErrorResult builds a CallToolResult flagged as an error, carrying
// the given text as its content. Matches the prior mark3labs
// NewToolResultError wire semantics (isError:true + text content).
func newErrorResult(msg string) *mcp.CallToolResult {
	r := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
	r.SetError(errors.New(msg))
	return r
}
