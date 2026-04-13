package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	feotel "github.com/hollis-labs/go-otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/go-toolbroker/broker"
)

// MCPTransport is the interface for MCP server connections (stdio or HTTP).
type MCPTransport interface {
	ListTools(ctx context.Context) ([]Tool, error)
	CallTool(ctx context.Context, name string, arguments map[string]any) (*ToolResult, error)
}

// ToolLoadChecker determines whether a tool should be included based on
// loadType configuration. This is set by the plugin host after building
// the override chain.
type ToolLoadChecker interface {
	IsToolEnabled(toolName string) bool
}

// Manager holds multiple MCP server connections and provides unified tool access.
type Manager struct {
	servers     map[string]MCPTransport // name -> transport
	tools       []toolEntry            // all discovered tools with server association
	Broker      *broker.LocalBroker    // intent-aware tool broker
	LoadChecker ToolLoadChecker        // optional loadType filter
	mu          sync.RWMutex
}

// toolEntry associates a tool with its originating server.
type toolEntry struct {
	serverName string
	tool       Tool
}

// NewManager creates a new MCP Manager.
func NewManager() *Manager {
	return &Manager{
		servers: make(map[string]MCPTransport),
	}
}

// AddServer registers an MCP server with the given transport.
// Call DiscoverTools() after adding all servers.
//
// Returns an error if name is empty, transport is nil, or a server with the
// same name is already registered. Silent overwrite is rejected because
// shadowing an existing MCP server is a footgun regardless of transport type
// (HTTP, stdio, plugin, or builtin).
func (m *Manager) AddServer(name string, transport MCPTransport) error {
	if name == "" {
		return fmt.Errorf("mcp: AddServer: name is required")
	}
	if transport == nil {
		return fmt.Errorf("mcp: AddServer %q: transport is nil", name)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.servers[name]; exists {
		return fmt.Errorf("mcp: AddServer %q: server already registered", name)
	}
	m.servers[name] = transport
	slog.Info("mcp: added server", "name", name)
	return nil
}

// DiscoverServerTools returns tools from a specific named server without affecting the global tool list.
func (m *Manager) DiscoverServerTools(ctx context.Context, serverName string) ([]Tool, error) {
	m.mu.RLock()
	transport, ok := m.servers[serverName]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("server %q not found", serverName)
	}
	return transport.ListTools(ctx)
}

// RemoveServer unregisters an MCP server, closing its transport if possible.
func (m *Manager) RemoveServer(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	transport, ok := m.servers[name]
	if !ok {
		return
	}

	// Close the transport if it supports it.
	if closer, ok := transport.(interface{ Close() error }); ok {
		closer.Close()
	}

	delete(m.servers, name)

	// Remove tools that belonged to this server.
	filtered := m.tools[:0]
	for _, entry := range m.tools {
		if entry.serverName != name {
			filtered = append(filtered, entry)
		}
	}
	m.tools = filtered

	slog.Info("mcp: removed server", "name", name)
}

// AddHTTPServer registers an HTTP-based MCP server. Propagates any error from
// AddServer (empty name, nil transport, duplicate registration).
func (m *Manager) AddHTTPServer(name, url string) error {
	if err := m.AddServer(name, NewHTTPTransport(url)); err != nil {
		return err
	}
	slog.Info("mcp: server using HTTP transport", "name", name, "url", url)
	return nil
}

// AddStdioServer registers a stdio-based MCP server (subprocess). Propagates
// any error from AddServer (empty name, nil transport, duplicate registration).
func (m *Manager) AddStdioServer(name, command string, args []string, env []string) error {
	if err := m.AddServer(name, NewStdioTransport(command, args, env)); err != nil {
		return err
	}
	slog.Info("mcp: server using stdio transport", "name", name, "command", command, "args", strings.Join(args, " "))
	return nil
}

// AddPluginServer registers an MCP server backed by a subprocess plugin's
// existing JSON-RPC transport. The plugin is expected to answer the
// mcp/list_tools and mcp/call_tool methods defined in plugin-sdk and to
// dispatch by the server name passed in params.
//
// Returns an error if name is empty, transport is nil, or the name is already
// registered. The explicit nil-transport guard here gives a clearer error
// message than letting NewPluginMCPTransport(nil, ...) wrap into a generic
// AddServer error; remaining validation is delegated to AddServer to keep the
// shared registration path in one place.
func (m *Manager) AddPluginServer(name string, transport *subprocess.Transport) error {
	if name == "" {
		return fmt.Errorf("mcp: AddPluginServer: name is required")
	}
	if transport == nil {
		return fmt.Errorf("mcp: AddPluginServer %q: transport is nil", name)
	}
	if err := m.AddServer(name, NewPluginMCPTransport(transport, name)); err != nil {
		return err
	}
	slog.Info("mcp: server using plugin transport", "name", name)
	return nil
}

// DiscoverTools queries all registered servers for their tools.
func (m *Manager) DiscoverTools(ctx context.Context) error {
	ctx, span := feotel.StartSpan(ctx, "nanite.mcp.discoverTools")
	defer span.End()

	m.mu.Lock()
	defer m.mu.Unlock()

	m.tools = nil
	var totalTools int

	for name, transport := range m.servers {
		tools, err := transport.ListTools(ctx)
		if err != nil {
			slog.Warn("mcp: failed to discover tools", "server", name, "err", err)
			continue
		}
		for _, t := range tools {
			m.tools = append(m.tools, toolEntry{
				serverName: name,
				tool:       t,
			})
		}
		totalTools += len(tools)
		slog.Info("mcp: discovered tools", "count", len(tools), "server", name)
	}

	// Register tools with the broker if available.
	if m.Broker != nil {
		var brokerTools []broker.ToolDefinition
		for _, entry := range m.tools {
			brokerTools = append(brokerTools, broker.ToolDefinition{
				Name:        entry.tool.Name,
				Description: entry.tool.Description,
				InputSchema: entry.tool.InputSchema,
				Server:      entry.serverName,
			})
		}
		m.Broker.RegisterTools(brokerTools)
		slog.Info("mcp: registered tools with broker", "count", len(brokerTools))
	}

	span.SetAttributes(
		attribute.Int("nanite.mcp.tools.total", totalTools),
		attribute.Int("nanite.mcp.servers.count", len(m.servers)),
	)

	slog.Info("mcp: discovery complete", "total_tools", totalTools, "servers", len(m.servers))
	return nil
}

// GetTools returns available tools as provider.ToolDefinition slice, filtered
// by the broker's default rules (intent "*"). Tool names are prefixed with "mcp__<server>__".
func (m *Manager) GetTools() []provider.ToolDefinition {
	return m.GetToolsForIntent("*", nil)
}

// GetToolsForIntent returns tools filtered by the broker for the given intent and hints.
// If no broker is configured, returns all tools unfiltered.
func (m *Manager) GetToolsForIntent(intent string, hints []string) []provider.ToolDefinition {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// If no broker, fall back to returning all tools.
	if m.Broker == nil {
		return m.getAllToolsLocked()
	}

	result, err := m.Broker.SelectTools(context.Background(), intent, hints)
	if err != nil {
		slog.Warn("mcp: broker SelectTools error — returning all tools", "err", err)
		return m.getAllToolsLocked()
	}

	// Convert broker ToolDefinitions back to provider.ToolDefinition with prefixed names.
	defs := make([]provider.ToolDefinition, 0, len(result.Tools))
	for _, t := range result.Tools {
		defs = append(defs, provider.ToolDefinition{
			Name:        fmt.Sprintf("mcp__%s__%s", t.Server, t.Name),
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}

	slog.Debug("mcp: broker selected tools", "count", result.Count, "total", result.Total, "intent", intent, "rationale", result.Rationale)
	return defs
}

// GetAllTools returns all enabled tools. Opt-in tools excluded unless enabled.
func (m *Manager) GetAllTools() []provider.ToolDefinition {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.getAllToolsLocked()
}

// getAllToolsLocked returns all enabled tools. Opt-in tools are excluded
// unless the LoadChecker says they are enabled. Caller must hold mu.RLock.
func (m *Manager) getAllToolsLocked() []provider.ToolDefinition {
	defs := make([]provider.ToolDefinition, 0, len(m.tools))
	for _, entry := range m.tools {
		qualifiedName := fmt.Sprintf("mcp__%s__%s", entry.serverName, entry.tool.Name)
		if m.LoadChecker != nil && !m.LoadChecker.IsToolEnabled(qualifiedName) {
			continue
		}
		defs = append(defs, provider.ToolDefinition{
			Name:        qualifiedName,
			Description: entry.tool.Description,
			InputSchema: entry.tool.InputSchema,
		})
	}
	return defs
}

// GetAllToolsUnfiltered returns every discovered tool regardless of loadType.
// Used for diagnostics and the tool load preferences UI.
func (m *Manager) GetAllToolsUnfiltered() []provider.ToolDefinition {
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

// ExecuteTool routes a tool call to the correct server and returns the result as text.
// Tool names are expected in the format "mcp__<server>__<tool_name>".
func (m *Manager) ExecuteTool(ctx context.Context, name string, input map[string]any) (string, error) {
	ctx, span := feotel.ToolCallSpan(ctx, name)
	defer span.End()

	serverName, toolName, err := parsePrefixedToolName(name)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}

	span.SetAttributes(
		attribute.String("nanite.mcp.server", serverName),
		attribute.String("nanite.mcp.tool", toolName),
	)

	m.mu.RLock()
	transport, ok := m.servers[serverName]
	m.mu.RUnlock()

	if !ok {
		err := fmt.Errorf("unknown MCP server: %s", serverName)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}

	result, err := transport.CallTool(ctx, toolName, input)
	if err != nil {
		err = fmt.Errorf("call tool %s on %s: %w", toolName, serverName, err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", err
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
		err := fmt.Errorf("tool error: %s", sb.String())
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}

	span.SetAttributes(attribute.Int("nanite.mcp.result_len", sb.Len()))
	return sb.String(), nil
}

// ResolveToolServer finds which server owns a bare (unprefixed) tool name.
// Returns the server name and prefixed tool name, or empty strings if not found.
func (m *Manager) ResolveToolServer(toolName string) (serverName, prefixedName string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, entry := range m.tools {
		if entry.tool.Name == toolName {
			return entry.serverName, fmt.Sprintf("mcp__%s__%s", entry.serverName, toolName)
		}
	}
	return "", ""
}

// HasTools reports whether any tools are available.
func (m *Manager) HasTools() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.tools) > 0
}

// ServerInfo describes an MCP server's status and tool count.
type ServerInfo struct {
	Name      string `json:"name"`
	ToolCount int    `json:"tool_count"`
	Connected bool   `json:"connected"`
}

// ListServers returns information about all registered MCP servers.
func (m *Manager) ListServers() []ServerInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Count tools per server.
	toolCounts := make(map[string]int)
	for _, entry := range m.tools {
		toolCounts[entry.serverName]++
	}

	infos := make([]ServerInfo, 0, len(m.servers))
	for name := range m.servers {
		infos = append(infos, ServerInfo{
			Name:      name,
			ToolCount: toolCounts[name],
			Connected: true, // registered means connected
		})
	}
	return infos
}

// DiscoveryDiff reports the changes found during auto-discovery.
type DiscoveryDiff struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
	Total   int      `json:"total"`
}

// AutoDiscover runs tool discovery and syncs results with the skills table.
// New tools get auto-created as skills (category="auto-discovered"),
// and tools that have disappeared are flagged.
func (m *Manager) AutoDiscover(ctx context.Context, s *store.Store) (*DiscoveryDiff, error) {
	// Run standard discovery first.
	if err := m.DiscoverTools(ctx); err != nil {
		return nil, fmt.Errorf("discover tools: %w", err)
	}

	diff := &DiscoveryDiff{}

	m.mu.RLock()
	currentTools := make(map[string]toolEntry, len(m.tools))
	for _, entry := range m.tools {
		prefixed := fmt.Sprintf("mcp__%s__%s", entry.serverName, entry.tool.Name)
		currentTools[prefixed] = entry
	}
	diff.Total = len(currentTools)
	m.mu.RUnlock()

	// Load existing auto-discovered skills from DB.
	existingSkills, err := s.ListSkills()
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}

	existingSlugs := make(map[string]*store.Skill, len(existingSkills))
	for i := range existingSkills {
		existingSlugs[existingSkills[i].Slug] = &existingSkills[i]
	}

	// Create skills for new tools.
	for prefixed, entry := range currentTools {
		slug := toolNameToSlug(prefixed)
		if _, exists := existingSlugs[slug]; exists {
			continue
		}

		sk := &store.Skill{
			Name:         entry.tool.Name,
			Slug:         slug,
			Description:  entry.tool.Description,
			Category:     "auto-discovered",
			ToolBindings: fmt.Sprintf(`[%q]`, entry.tool.Name),
			IsBuiltin:    false,
			Settings:     fmt.Sprintf(`{"server":%q,"auto_discovered":true}`, entry.serverName),
		}
		if err := s.CreateSkill(sk); err != nil {
			slog.Warn("mcp: auto-discover failed to create skill", "slug", slug, "err", err)
			continue
		}
		diff.Added = append(diff.Added, prefixed)
		slog.Info("mcp: auto-discovered new tool → skill", "slug", slug)
	}

	// Flag removed tools by updating their settings.
	for slug, sk := range existingSlugs {
		if sk.Category != "auto-discovered" {
			continue
		}
		// Check if any current tool matches this slug.
		found := false
		for prefixed := range currentTools {
			if toolNameToSlug(prefixed) == slug {
				found = true
				break
			}
		}
		if !found {
			// Mark as removed in settings.
			sk.Settings = strings.Replace(sk.Settings, `"auto_discovered":true`, `"auto_discovered":true,"removed":true`, 1)
			if err := s.UpdateSkill(sk); err != nil {
				slog.Warn("mcp: auto-discover failed to flag removed skill", "slug", slug, "err", err)
			}
			diff.Removed = append(diff.Removed, slug)
			slog.Info("mcp: auto-discover flagged removed tool", "slug", slug)
		}
	}

	slog.Info("mcp: auto-discovery complete",
		"total", diff.Total, "added", len(diff.Added), "removed", len(diff.Removed))
	return diff, nil
}

// toolNameToSlug converts a prefixed tool name to a URL-safe slug.
func toolNameToSlug(name string) string {
	slug := strings.ReplaceAll(name, "__", "-")
	slug = strings.ReplaceAll(slug, "_", "-")
	return slug
}

// Close shuts down all transports that implement io.Closer.
//
// Inner transport Close() calls are made WITHOUT holding m.mu — the transport
// Close may block on subprocess exit, and holding the manager mutex across it
// would deadlock any concurrent RLock caller (GetTools, ExecuteTool, etc.).
// We snapshot the transport list under lock, then release and call Close on
// each one.
func (m *Manager) Close() {
	m.mu.Lock()
	type namedTransport struct {
		name      string
		transport MCPTransport
	}
	snapshot := make([]namedTransport, 0, len(m.servers))
	for name, t := range m.servers {
		snapshot = append(snapshot, namedTransport{name: name, transport: t})
	}
	m.mu.Unlock()

	for _, nt := range snapshot {
		if closer, ok := nt.transport.(interface{ Close() error }); ok {
			closer.Close()
			slog.Info("mcp: closed transport", "server", nt.name)
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
