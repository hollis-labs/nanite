package mcp

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/hollis-labs/mentat-chat/internal/provider"
)

// Transport is the interface for MCP server connections (stdio or HTTP).
type Transport interface {
	ListTools(ctx context.Context) ([]Tool, error)
	CallTool(ctx context.Context, name string, arguments map[string]any) (*ToolResult, error)
}

// Manager holds multiple MCP server connections and provides unified tool access.
type Manager struct {
	servers map[string]Transport // name -> transport
	tools   []toolEntry          // all discovered tools with server association
	mu      sync.RWMutex
}

// toolEntry associates a tool with its originating server.
type toolEntry struct {
	serverName string
	tool       Tool
}

// NewManager creates a new MCP Manager.
func NewManager() *Manager {
	return &Manager{
		servers: make(map[string]Transport),
	}
}

// AddServer registers an MCP server with the given transport.
// Call DiscoverTools() after adding all servers.
func (m *Manager) AddServer(name string, transport Transport) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.servers[name] = transport
	log.Printf("mcp: added server %q", name)
}

// AddHTTPServer registers an HTTP-based MCP server.
func (m *Manager) AddHTTPServer(name, url string) {
	m.AddServer(name, NewHTTPTransport(url))
	log.Printf("mcp: server %q using HTTP transport at %s", name, url)
}

// AddStdioServer registers a stdio-based MCP server (subprocess).
func (m *Manager) AddStdioServer(name, command string, args []string, env []string) {
	m.AddServer(name, NewStdioTransport(command, args, env))
	log.Printf("mcp: server %q using stdio transport: %s %s", name, command, strings.Join(args, " "))
}

// DiscoverTools queries all registered servers for their tools.
func (m *Manager) DiscoverTools(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.tools = nil
	var totalTools int

	for name, transport := range m.servers {
		tools, err := transport.ListTools(ctx)
		if err != nil {
			log.Printf("mcp: failed to discover tools from %s: %v", name, err)
			continue
		}
		for _, t := range tools {
			m.tools = append(m.tools, toolEntry{
				serverName: name,
				tool:       t,
			})
		}
		totalTools += len(tools)
		log.Printf("mcp: discovered %d tools from %s", len(tools), name)
	}

	// Count how many will actually be exposed to the LLM.
	var excluded int
	for _, entry := range m.tools {
		if m.isExcluded(entry.tool.Name) {
			excluded++
		}
	}
	log.Printf("mcp: total %d tools from %d servers (%d exposed to LLM, %d filtered)", totalTools, len(m.servers), totalTools-excluded, excluded)
	return nil
}

// defaultExcludePatterns filters out tools that bloat the LLM context.
// These tools are still callable via ExecuteTool but not advertised to the LLM.
// The agent can discover them via list/search tools on each server.
var defaultExcludePatterns = []string{
	"hadron_bp_", // 70+ individual blueprint tools — use hadron_blueprints_list + hadron_run_enqueue instead
}

// GetTools returns available tools as provider.ToolDefinition slice, filtered
// to exclude context-heavy patterns. Tool names are prefixed with "mcp__<server>__".
func (m *Manager) GetTools() []provider.ToolDefinition {
	m.mu.RLock()
	defer m.mu.RUnlock()

	defs := make([]provider.ToolDefinition, 0, len(m.tools))
	for _, entry := range m.tools {
		if m.isExcluded(entry.tool.Name) {
			continue
		}
		defs = append(defs, provider.ToolDefinition{
			Name:        fmt.Sprintf("mcp__%s__%s", entry.serverName, entry.tool.Name),
			Description: entry.tool.Description,
			InputSchema: entry.tool.InputSchema,
		})
	}
	return defs
}

// GetAllTools returns ALL tools including excluded ones. Used for diagnostics.
func (m *Manager) GetAllTools() []provider.ToolDefinition {
	m.mu.RLock()
	defer m.mu.RUnlock()

	defs := make([]provider.ToolDefinition, 0, len(m.tools))
	for _, entry := range m.tools {
		defs = append(defs, provider.ToolDefinition{
			Name:        fmt.Sprintf("mcp__%s__%s", entry.serverName, entry.tool.Name),
			Description: entry.tool.Description,
			InputSchema: entry.tool.InputSchema,
		})
	}
	return defs
}

// isExcluded checks if a tool name matches any exclude pattern.
func (m *Manager) isExcluded(name string) bool {
	for _, pattern := range defaultExcludePatterns {
		if strings.HasPrefix(name, pattern) {
			return true
		}
	}
	return false
}

// ExecuteTool routes a tool call to the correct server and returns the result as text.
// Tool names are expected in the format "mcp__<server>__<tool_name>".
func (m *Manager) ExecuteTool(ctx context.Context, name string, input map[string]any) (string, error) {
	serverName, toolName, err := parsePrefixedToolName(name)
	if err != nil {
		return "", err
	}

	m.mu.RLock()
	transport, ok := m.servers[serverName]
	m.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("unknown MCP server: %s", serverName)
	}

	result, err := transport.CallTool(ctx, toolName, input)
	if err != nil {
		return "", fmt.Errorf("call tool %s on %s: %w", toolName, serverName, err)
	}

	// Concatenate text content blocks.
	var sb strings.Builder
	for _, c := range result.Content {
		if c.Type == "text" && c.Text != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(c.Text)
		}
	}

	if result.IsError {
		return "", fmt.Errorf("tool error: %s", sb.String())
	}

	return sb.String(), nil
}

// HasTools reports whether any tools are available.
func (m *Manager) HasTools() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.tools) > 0
}

// Close shuts down all transports that implement io.Closer.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, transport := range m.servers {
		if closer, ok := transport.(interface{ Close() error }); ok {
			closer.Close()
			log.Printf("mcp: closed transport for %s", name)
		}
	}
}

// parsePrefixedToolName splits "mcp__server__tool_name" into server and tool name.
func parsePrefixedToolName(name string) (server, tool string, err error) {
	if !strings.HasPrefix(name, "mcp__") {
		return "", "", fmt.Errorf("tool name %q missing mcp__ prefix", name)
	}
	rest := strings.TrimPrefix(name, "mcp__")
	idx := strings.Index(rest, "__")
	if idx < 0 {
		return "", "", fmt.Errorf("tool name %q missing server separator", name)
	}
	server = rest[:idx]
	tool = rest[idx+2:]
	if server == "" || tool == "" {
		return "", "", fmt.Errorf("tool name %q has empty server or tool", name)
	}
	return server, tool, nil
}
