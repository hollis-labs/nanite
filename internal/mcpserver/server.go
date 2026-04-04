package mcpserver

import (
	"context"
	"log"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

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
	srv := server.NewMCPServer(
		brand.ID,
		version.Version,
		server.WithToolCapabilities(true),
	)
	s.registerTools(srv)
	log.Printf("mcpserver: starting stdio server (session=%s)", s.sessionID)
	ctxFunc := func(_ context.Context) context.Context { return ctx }
	return server.ServeStdio(srv, server.WithStdioContextFunc(ctxFunc))
}

// registerTools adds all self-service tool definitions to the MCP server.
func (s *Server) registerTools(srv *server.MCPServer) {
	tools, err := s.self.ListTools(context.Background())
	if err != nil {
		log.Printf("mcpserver: failed to list tools: %v", err)
		return
	}

	for _, t := range tools {
		tool := buildMCPTool(t)
		name := t.Name
		srv.AddTool(tool, s.makeHandler(name))
	}
	log.Printf("mcpserver: registered %d tools", len(tools))
}

// buildMCPTool converts a Nanite Tool definition to a mcp-go Tool.
func buildMCPTool(t condmcp.Tool) mcp.Tool {
	return mcp.NewToolWithRawSchema(t.Name, t.Description, mustMarshalSchema(t.InputSchema))
}

// makeHandler returns a ToolHandlerFunc that delegates to SelfToolsTransport.
func (s *Server) makeHandler(name string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		result, err := s.self.CallTool(ctx, name, args)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		text := extractText(result)
		text = convertEnvelopeMarkers(text)
		return mcp.NewToolResultText(text), nil
	}
}
