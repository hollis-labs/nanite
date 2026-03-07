package mcp

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/hollis-labs/mentat-chat/internal/provider"
)

// Manager holds multiple MCP server connections and provides unified tool access.
type Manager struct {
	servers map[string]*HTTPTransport // name -> transport
	tools   []toolEntry              // all discovered tools with server association
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
		servers: make(map[string]*HTTPTransport),
	}
}

// AddServer registers an MCP server by name and URL.
// Call DiscoverTools() after adding all servers.
func (m *Manager) AddServer(name, url string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.servers[name] = NewHTTPTransport(url)
	log.Printf("mcp: added server %q at %s", name, url)
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

	log.Printf("mcp: total %d tools from %d servers", totalTools, len(m.servers))
	return nil
}

// GetTools returns all available tools as provider.ToolDefinition slice.
// Tool names are prefixed with "mcp__<server>__" to match Claude Code convention.
func (m *Manager) GetTools() []provider.ToolDefinition {
	m.mu.RLock()
	defer m.mu.RUnlock()

	defs := make([]provider.ToolDefinition, len(m.tools))
	for i, entry := range m.tools {
		defs[i] = provider.ToolDefinition{
			Name:        fmt.Sprintf("mcp__%s__%s", entry.serverName, entry.tool.Name),
			Description: entry.tool.Description,
			InputSchema: entry.tool.InputSchema,
		}
	}
	return defs
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
