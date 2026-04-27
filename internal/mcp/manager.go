package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
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

// DiscoveryWarning records a tool that was rejected during MCP discovery.
type DiscoveryWarning struct {
	ServerName string `json:"server_name"`
	ToolName   string `json:"tool_name"`
	Reason     string `json:"reason"`
}

// Manager holds multiple MCP server connections and provides unified tool access.
//
// Uniform tool registry (CW-20260427-0017, ADR-002): every MCP tool is
// registered under a uniform agent-facing name (no `mcp__server__` prefix).
// The mapping uniform-name → (server, original tool) is owned by the
// uniformIndex map; ExecuteTool / ResolveToolServer go through it. The
// originating server is preserved as audit metadata so observability
// pipelines still know which MCP backend a call hit, while the agent only
// ever sees the uniform name.
type Manager struct {
	servers           map[string]MCPTransport // name -> transport
	serverTiers       map[string]TrustTier    // name -> trust tier
	pluginServers     map[string][]string     // pluginID -> server names (reverse map for hot-unload)
	tools             []toolEntry             // all discovered tools with server association
	uniformIndex      map[string]*toolEntry   // uniform name → entry (owns the *toolEntry)
	discoveryWarnings []DiscoveryWarning      // tools rejected during discovery
	Broker            *broker.LocalBroker     // intent-aware tool broker
	LoadChecker       ToolLoadChecker         // optional loadType filter
	mu                sync.RWMutex
}

// toolEntry associates a tool with its originating server and the
// uniform agent-facing name assigned at registration time.
type toolEntry struct {
	serverName  string
	uniformName string // agent-facing name; never carries `mcp__` prefix
	tool        Tool
}

// NewManager creates a new MCP Manager.
func NewManager() *Manager {
	return &Manager{
		servers:       make(map[string]MCPTransport),
		serverTiers:   make(map[string]TrustTier),
		pluginServers: make(map[string][]string),
		uniformIndex:  make(map[string]*toolEntry),
	}
}

// AddServer registers an MCP server with the given transport and trust tier.
// Call DiscoverTools() after adding all servers.
//
// The tier classifies the server's blast radius (S4b D1) and selects per-tier
// validator limits used at discovery and execution time. If the transport
// implements an int "SetMaxResponseBytes" setter, the tier-derived
// MaxResultBytes is wired into the transport so the per-line / per-body
// reader cap tightens from the default 10 MiB safety net to the tier ceiling.
//
// Returns an error if name is empty, transport is nil, or a server with the
// same name is already registered. Silent overwrite is rejected because
// shadowing an existing MCP server is a footgun regardless of transport type
// (HTTP, stdio, plugin, or builtin).
func (m *Manager) AddServer(name string, transport MCPTransport, tier TrustTier) error {
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
	m.serverTiers[name] = tier

	// Tighten the transport's response cap to the tier ceiling when the
	// transport opts in via the SetMaxResponseBytes interface (HTTPTransport
	// and StdioTransport do; in-process built-ins don't need to).
	if setter, ok := transport.(interface{ SetMaxResponseBytes(int) }); ok {
		setter.SetMaxResponseBytes(LimitsFor(tier).MaxResultBytes)
	}

	slog.Info("mcp: added server", "name", name, "tier", string(tier))
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
	delete(m.serverTiers, name)

	// Remove tools that belonged to this server (in both the slice view
	// and the uniform-name index).
	filtered := m.tools[:0]
	for _, entry := range m.tools {
		if entry.serverName != name {
			filtered = append(filtered, entry)
		} else {
			delete(m.uniformIndex, entry.uniformName)
		}
	}
	m.tools = filtered

	slog.Info("mcp: removed server", "name", name)
}

// AddHTTPServer registers an HTTP-based MCP server with the given trust tier.
// Propagates any error from AddServer (empty name, nil transport, duplicate
// registration).
func (m *Manager) AddHTTPServer(name, url string, tier TrustTier) error {
	if err := m.AddServer(name, NewHTTPTransport(url), tier); err != nil {
		return err
	}
	slog.Info("mcp: server using HTTP transport", "name", name, "url", url, "tier", string(tier))
	return nil
}

// AddStdioServer registers a stdio-based MCP server (subprocess) with the
// given trust tier and host-env allowlist. envAllowlist is the list of
// environment variable names the subprocess is permitted to inherit from
// nanite's own environment (S4b D6 / finding 10). Pass nil/empty for
// no inheritance. PATH must always be in the allowlist — the transport
// fails at start-time otherwise. Propagates any error from AddServer
// (empty name, nil transport, duplicate registration).
func (m *Manager) AddStdioServer(name, command string, args []string, env []string, envAllowlist []string, tier TrustTier) error {
	if err := m.AddServer(name, NewStdioTransport(command, args, env, envAllowlist), tier); err != nil {
		return err
	}
	slog.Info("mcp: server using stdio transport",
		"name", name,
		"command", command,
		"args", strings.Join(args, " "),
		"tier", string(tier),
		"env_allowlist", envAllowlist,
	)
	return nil
}

// AddPluginServer registers an MCP server backed by a subprocess plugin's
// existing JSON-RPC transport. The plugin is expected to answer the
// mcp/list_tools and mcp/call_tool methods defined in plugin-sdk and to
// dispatch by the server name passed in params.
//
// Plugin servers are always subprocess-backed, so the trust tier is fixed
// at TierPluginStdio (S4b D1). Keeping this implicit avoids leaking the
// mcp.TrustTier type into internal/plugin, which would close an import
// cycle (mcp → plugin/subprocess and plugin → mcp).
//
// Returns an error if name is empty, transport is nil, or the name is already
// registered. The explicit nil-transport guard here gives a clearer error
// message than letting NewPluginMCPTransport(nil, ...) wrap into a generic
// AddServer error; remaining validation is delegated to AddServer to keep the
// shared registration path in one place.
//
// The pluginID argument is recorded in a reverse map so the host can drop
// every server owned by a plugin during UnloadPlugin. An empty pluginID is
// accepted (tests and ad-hoc callers) and simply skips reverse-map tracking.
func (m *Manager) AddPluginServer(pluginID, name string, transport *subprocess.Transport) error {
	if name == "" {
		return fmt.Errorf("mcp: AddPluginServer: name is required")
	}
	if transport == nil {
		return fmt.Errorf("mcp: AddPluginServer %q: transport is nil", name)
	}
	if err := m.AddServer(name, NewPluginMCPTransport(transport, name), TierPluginStdio); err != nil {
		return err
	}
	if pluginID != "" {
		m.mu.Lock()
		m.pluginServers[pluginID] = append(m.pluginServers[pluginID], name)
		m.mu.Unlock()
	}
	slog.Info("mcp: server using plugin transport", "name", name, "plugin", pluginID, "tier", string(TierPluginStdio))
	return nil
}

// RemoveServersByPlugin removes every MCP server owned by pluginID. Returns
// the number of servers removed. Safe to call when the plugin registered zero
// servers (returns 0).
func (m *Manager) RemoveServersByPlugin(pluginID string) int {
	if pluginID == "" {
		return 0
	}
	m.mu.Lock()
	names := m.pluginServers[pluginID]
	delete(m.pluginServers, pluginID)
	m.mu.Unlock()

	for _, name := range names {
		m.RemoveServer(name)
	}
	return len(names)
}

// tierFor returns the registered tier for a server, defaulting to
// TierThirdPartyHTTP (D4 fail-closed) when unknown. Caller must hold mu.
func (m *Manager) tierForLocked(name string) TrustTier {
	if t, ok := m.serverTiers[name]; ok && t != "" {
		return t
	}
	return TierThirdPartyHTTP
}

// DiscoverTools queries all registered servers for their tools and runs the
// per-tier validator pipeline (S4b T2). Tools failing per-tool validation
// (ValidateToolMeta) are skipped with a DiscoveryWarning. Cross-tool checks
// (ValidateToolSet — count cap, duplicate names) emit the same warnings;
// when the count cap fires, only the first MaxToolsPerServer tools are
// retained.
func (m *Manager) DiscoverTools(ctx context.Context) error {
	ctx, span := feotel.StartSpan(ctx, "nanite.mcp.discoverTools")
	defer span.End()

	m.mu.Lock()
	defer m.mu.Unlock()

	m.tools = nil
	m.uniformIndex = make(map[string]*toolEntry)
	m.discoveryWarnings = nil
	var totalTools int

	// Iterate servers in deterministic name order so collision resolution
	// is reproducible across runs (without ordering, two servers with the
	// same tool name would race for the uniform slot).
	serverNames := make([]string, 0, len(m.servers))
	for name := range m.servers {
		serverNames = append(serverNames, name)
	}
	sort.Strings(serverNames)

	for _, name := range serverNames {
		transport := m.servers[name]
		tier := m.tierForLocked(name)
		limits := LimitsFor(tier)

		tools, err := transport.ListTools(ctx)
		if err != nil {
			slog.Warn("mcp: failed to discover tools", "server", name, "err", err)
			continue
		}

		// Cross-tool checks first so the count cap can be applied before we
		// iterate. Each ValidationError → one DiscoveryWarning.
		for _, ve := range ValidateToolSet(tier, tools) {
			slog.Warn("mcp: discovery cross-tool check",
				"server", name, "field", ve.Field, "reason", ve.Reason)
			m.discoveryWarnings = append(m.discoveryWarnings, DiscoveryWarning{
				ServerName: name,
				ToolName:   ve.Value, // empty for count-cap; tool name for dup
				Reason:     ve.Field,
			})
		}
		if len(tools) > limits.MaxToolsPerServer {
			tools = tools[:limits.MaxToolsPerServer]
		}

		advertised := len(tools)
		accepted := 0
		seen := make(map[string]struct{}, len(tools))
		for _, t := range tools {
			// Skip duplicates here too so the in-scope tool list mirrors the
			// validator outcome (ValidateToolSet flagged them already).
			if _, dup := seen[t.Name]; dup {
				continue
			}
			seen[t.Name] = struct{}{}

			if errs := ValidateToolMeta(tier, t); len(errs) > 0 {
				for _, ve := range errs {
					slog.Warn("mcp: tool rejected at discovery",
						"server", name, "tool", t.Name, "field", ve.Field, "reason", ve.Reason)
					m.discoveryWarnings = append(m.discoveryWarnings, DiscoveryWarning{
						ServerName: name,
						ToolName:   t.Name,
						Reason:     ve.Field,
					})
				}
				continue
			}

			// Compute the uniform agent-facing name and resolve collisions.
			uniform := m.assignUniformNameLocked(name, t.Name)
			if uniform == "" {
				slog.Warn("mcp: tool dropped (uniform name collision unresolvable)",
					"server", name, "tool", t.Name)
				m.discoveryWarnings = append(m.discoveryWarnings, DiscoveryWarning{
					ServerName: name,
					ToolName:   t.Name,
					Reason:     "uniform_name_collision",
				})
				continue
			}

			entry := &toolEntry{
				serverName:  name,
				uniformName: uniform,
				tool:        t,
			}
			m.tools = append(m.tools, *entry)
			m.uniformIndex[uniform] = &m.tools[len(m.tools)-1]
			accepted++
			totalTools++
		}

		slog.Info("mcp: discovered tools",
			"server", name,
			"tier", string(tier),
			"advertised", advertised,
			"accepted", accepted,
		)
	}

	// Register tools with the broker under their uniform agent-facing
	// names. Server attribution is preserved on the broker.ToolDefinition
	// so audit/telemetry retains source-server context, but the broker's
	// tool index is keyed by the uniform name the agent will see.
	if m.Broker != nil {
		var brokerTools []broker.ToolDefinition
		for i := range m.tools {
			entry := &m.tools[i]
			brokerTools = append(brokerTools, broker.ToolDefinition{
				Name:        entry.uniformName,
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
// by the broker's default rules (intent "*"). Names are uniform (no
// `mcp__server__` prefix) — see ADR-002.
func (m *Manager) GetTools() []provider.ToolDefinition {
	return m.GetToolsForIntent("*", nil)
}

// GetToolsForIntent returns tools filtered by the broker for the given intent and hints.
// If no broker is configured, returns all tools unfiltered. Names are uniform.
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

	// The broker is registered with uniform names already (see
	// DiscoverTools); selection results carry uniform names directly.
	defs := make([]provider.ToolDefinition, 0, len(result.Tools))
	for _, t := range result.Tools {
		defs = append(defs, provider.ToolDefinition{
			Name:        t.Name,
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
		if m.LoadChecker != nil && !m.LoadChecker.IsToolEnabled(entry.uniformName) {
			continue
		}
		defs = append(defs, provider.ToolDefinition{
			Name:        entry.uniformName,
			Description: entry.tool.Description,
			InputSchema: entry.tool.InputSchema,
		})
	}
	return defs
}

// GetAllToolsUnfiltered returns every discovered tool regardless of loadType.
// Used for diagnostics and the tool load preferences UI. Names are uniform.
func (m *Manager) GetAllToolsUnfiltered() []provider.ToolDefinition {
	m.mu.RLock()
	defer m.mu.RUnlock()
	defs := make([]provider.ToolDefinition, 0, len(m.tools))
	for _, entry := range m.tools {
		defs = append(defs, provider.ToolDefinition{
			Name:        entry.uniformName,
			Description: entry.tool.Description,
			InputSchema: entry.tool.InputSchema,
		})
	}
	return defs
}

// ToolAttribution returns the originating MCP server and the tool's
// original name on that server, given a uniform agent-facing name.
// Returns ok=false when the name is not a registered MCP tool. Used by
// audit / telemetry sites that need source-server context after the
// agent-facing surface has been flattened.
func (m *Manager) ToolAttribution(uniformName string) (server, originalTool string, ok bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, found := m.uniformIndex[uniformName]
	if !found {
		return "", "", false
	}
	return entry.serverName, entry.tool.Name, true
}

// HasServer reports whether a server with the given name is registered.
// Replaces the legacy "scan tools for `mcp__<server>__` prefix" check
// that callers used to detect server presence.
func (m *Manager) HasServer(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.servers[name]
	return ok
}

// assignUniformNameLocked computes the uniform agent-facing name for a
// tool, resolving collisions against the existing uniformIndex.
//
// Resolution order:
//  1. Default — bare tool name (`memory_write` from `mux`).
//  2. Reserved-namespace defense — if the bare name lives in `nanite_*`,
//     force-prefix with the server (`<server>_<tool>`) regardless of any
//     collision. The nanite_ namespace is exclusive to first-party
//     self-tools.
//  3. Collision — if the default slot is already occupied by a tool from
//     a different server, fall back to `<server>_<tool>` and try that
//     slot. Both the incumbent and the newcomer keep their disambiguated
//     forms; the bare slot is freed (the incumbent is rewritten too).
//  4. If the disambiguated slot is also occupied, drop the tool with a
//     warning — this is a hard collision (two servers exporting the same
//     `<server>_<tool>` shape, which can only happen if servers share a
//     name, which AddServer rejects, OR if a server's tool name embeds
//     another server's name as a prefix). Returns "".
//
// Caller holds m.mu.
func (m *Manager) assignUniformNameLocked(serverName, toolName string) string {
	bare := UniformToolName(serverName, toolName)
	if bare == "" {
		return ""
	}

	// (2) Reserved-namespace defense.
	if IsReservedSelfToolName(bare) {
		disambig := DisambiguatedToolName(serverName, toolName)
		if _, taken := m.uniformIndex[disambig]; taken {
			return ""
		}
		slog.Warn("mcp: third-party tool name collides with reserved nanite_ namespace; force-prefixed",
			"server", serverName, "tool", toolName, "uniform", disambig)
		return disambig
	}

	// (1) Default slot.
	if existing, taken := m.uniformIndex[bare]; !taken {
		return bare
	} else if existing.serverName == serverName {
		// Same-server duplicate (validators above should have caught it,
		// but be defensive). Return empty to drop.
		return ""
	} else {
		// (3) Collision — rewrite the incumbent into its disambiguated
		// slot, then place the newcomer in its own disambiguated slot.
		incumbentDisambig := DisambiguatedToolName(existing.serverName, existing.tool.Name)
		newcomerDisambig := DisambiguatedToolName(serverName, toolName)

		// Hard-collision guard: incumbent already has a same-shape slot,
		// or newcomer's disambiguated slot is taken by yet another tool.
		if _, taken := m.uniformIndex[incumbentDisambig]; taken {
			if existing.uniformName != incumbentDisambig {
				return ""
			}
		}
		if _, taken := m.uniformIndex[newcomerDisambig]; taken {
			return ""
		}

		// Rewrite the incumbent.
		delete(m.uniformIndex, bare)
		existing.uniformName = incumbentDisambig
		m.uniformIndex[incumbentDisambig] = existing
		slog.Warn("mcp: tool-name collision — disambiguated both servers",
			"name", bare,
			"incumbent_server", existing.serverName,
			"incumbent_uniform", incumbentDisambig,
			"newcomer_server", serverName,
			"newcomer_uniform", newcomerDisambig,
		)

		// Record collision warnings on each affected server.
		m.discoveryWarnings = append(m.discoveryWarnings, DiscoveryWarning{
			ServerName: existing.serverName,
			ToolName:   existing.tool.Name,
			Reason:     "uniform_name_collision_disambiguated",
		})
		m.discoveryWarnings = append(m.discoveryWarnings, DiscoveryWarning{
			ServerName: serverName,
			ToolName:   toolName,
			Reason:     "uniform_name_collision_disambiguated",
		})

		return newcomerDisambig
	}
}

// ExecuteTool routes a tool call to the correct server and returns the
// result as text. The tool name is the uniform agent-facing name (no
// `mcp__server__` prefix); the manager looks up the originating server
// via the uniform-name index.
//
// Result handling (S4b T3 + T5 + D5):
//  1. ValidateBlockType drops blocks whose Type is not in the allowlist.
//  2. StripANSI strips CSI/OSC sequences from text blocks (UI spoofing).
//  3. ScanInjection emits WARN logs + structured metric records on hits;
//     S4b is observability-only (D3), so hits do not block the call.
//  4. ValidateResultSize enforces the per-tier MaxResultBytes ceiling on
//     the assembled string as defense-in-depth on top of the transport's
//     own LimitReader cap (S4b T3 transport-level wiring + PR #42 floor).
func (m *Manager) ExecuteTool(ctx context.Context, name string, input map[string]any) (string, error) {
	ctx, span := feotel.ToolCallSpan(ctx, name)
	defer span.End()

	m.mu.RLock()
	entry, found := m.uniformIndex[name]
	var (
		serverName string
		toolName   string
		transport  MCPTransport
		tier       TrustTier
		ok         bool
	)
	if found {
		serverName = entry.serverName
		toolName = entry.tool.Name
		transport, ok = m.servers[serverName]
		tier = m.tierForLocked(serverName)
	}
	m.mu.RUnlock()

	if !found {
		err := fmt.Errorf("unknown MCP tool: %s", name)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}

	span.SetAttributes(
		attribute.String("nanite.mcp.server", serverName),
		attribute.String("nanite.mcp.tool", toolName),
		attribute.String("nanite.mcp.uniform_name", name),
	)

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

	out, err := assembleToolResultText(serverName, toolName, result)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}
	if err := ValidateResultSize(tier, len(out)); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", fmt.Errorf("call tool %s on %s: %w", toolName, serverName, err)
	}

	span.SetAttributes(attribute.Int("nanite.mcp.result_len", len(out)))
	return out, nil
}

// ResolveToolServer finds which server owns a uniform agent-facing tool
// name. Returns the server name and the same uniform name (the
// "prefixed" return retained for compat with the historical signature),
// or empty strings if not found.
func (m *Manager) ResolveToolServer(uniformName string) (serverName, resolvedName string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if entry, ok := m.uniformIndex[uniformName]; ok {
		return entry.serverName, entry.uniformName
	}
	return "", ""
}

// ExecuteToolOnServer is the explicit-server execution path used by
// internal callers that know which server they want to talk to (the
// chat orchestrator's Engine sprint creator, the contextbroker sources,
// etc.) — they should NOT have to look up a uniform name to reach a
// known tool. Bypasses the uniform-name index but still applies the
// trust-tier validation pipeline.
func (m *Manager) ExecuteToolOnServer(ctx context.Context, serverName, toolName string, input map[string]any) (string, error) {
	if toolName == "" {
		return "", fmt.Errorf("mcp: ExecuteToolOnServer: empty tool name")
	}
	uniform := UniformToolName(serverName, toolName)
	ctx, span := feotel.ToolCallSpan(ctx, uniform)
	defer span.End()

	m.mu.RLock()
	transport, ok := m.servers[serverName]
	tier := m.tierForLocked(serverName)
	m.mu.RUnlock()
	if !ok {
		err := fmt.Errorf("unknown MCP server: %s", serverName)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}

	span.SetAttributes(
		attribute.String("nanite.mcp.server", serverName),
		attribute.String("nanite.mcp.tool", toolName),
	)

	result, err := transport.CallTool(ctx, toolName, input)
	if err != nil {
		err = fmt.Errorf("call tool %s on %s: %w", toolName, serverName, err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}

	out, err := assembleToolResultText(serverName, toolName, result)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}
	if err := ValidateResultSize(tier, len(out)); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", fmt.Errorf("call tool %s on %s: %w", toolName, serverName, err)
	}
	span.SetAttributes(attribute.Int("nanite.mcp.result_len", len(out)))
	return out, nil
}

// assembleToolResultText concatenates valid text blocks from a tool
// result, applying ANSI stripping and injection-pattern observability.
// It returns an error when the result is flagged IsError.
func assembleToolResultText(serverName, toolName string, result *ToolResult) (string, error) {
	var sb strings.Builder
	for _, c := range result.Content {
		if err := ValidateBlockType(c); err != nil {
			slog.Warn("mcp: dropping invalid content block",
				"server", serverName, "tool", toolName, "block_type", c.Type)
			continue
		}
		if c.Type != "text" || c.Text == "" {
			continue
		}
		text := StripANSI(c.Text)
		for _, hit := range ScanInjection(text) {
			slog.Warn("mcp: injection pattern detected",
				"server", serverName,
				"tool", toolName,
				"rule", hit.Rule,
				"snippet", hit.Snippet,
			)
			slog.Info("metric mcp_injection_hits_total",
				"server", serverName,
				"tool", toolName,
				"rule", hit.Rule,
				"count", 1,
			)
		}
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(text)
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

// ServerInfo describes an MCP server's status and tool count.
type ServerInfo struct {
	Name              string             `json:"name"`
	ToolCount         int                `json:"tool_count"`
	Connected         bool               `json:"connected"`
	TrustTier         string             `json:"trust_tier,omitempty"`
	DiscoveryWarnings []DiscoveryWarning `json:"discovery_warnings,omitempty"`
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

	// Collect per-server discovery warnings.
	serverWarnings := make(map[string][]DiscoveryWarning)
	for _, w := range m.discoveryWarnings {
		serverWarnings[w.ServerName] = append(serverWarnings[w.ServerName], w)
	}

	infos := make([]ServerInfo, 0, len(m.servers))
	for name := range m.servers {
		infos = append(infos, ServerInfo{
			Name:              name,
			ToolCount:         toolCounts[name],
			Connected:         true, // registered means connected
			TrustTier:         string(m.tierForLocked(name)),
			DiscoveryWarnings: serverWarnings[name],
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

	// currentTools is keyed by uniform agent-facing name (post
	// internalization). The originating server is captured on each entry
	// for skill metadata (audit attribution).
	m.mu.RLock()
	currentTools := make(map[string]toolEntry, len(m.tools))
	for _, entry := range m.tools {
		currentTools[entry.uniformName] = entry
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

	// Create skills for new tools. The skill slug is derived from the
	// uniform agent-facing name; tool_bindings record the same uniform
	// name so callers that resolve the binding can dispatch directly.
	for uniform, entry := range currentTools {
		slug := toolNameToSlug(uniform)
		if _, exists := existingSlugs[slug]; exists {
			continue
		}

		sk := &store.Skill{
			Name:         entry.tool.Name,
			Slug:         slug,
			Description:  entry.tool.Description,
			Category:     "auto-discovered",
			ToolBindings: fmt.Sprintf(`[%q]`, uniform),
			IsBuiltin:    false,
			Settings:     fmt.Sprintf(`{"server":%q,"auto_discovered":true}`, entry.serverName),
		}
		if err := s.CreateSkill(sk); err != nil {
			slog.Warn("mcp: auto-discover failed to create skill", "slug", slug, "err", err)
			continue
		}
		diff.Added = append(diff.Added, uniform)
		slog.Info("mcp: auto-discovered new tool → skill", "slug", slug)
	}

	// Flag removed tools by updating their settings.
	for slug, sk := range existingSlugs {
		if sk.Category != "auto-discovered" {
			continue
		}
		// Check if any current tool matches this slug.
		found := false
		for uniform := range currentTools {
			if toolNameToSlug(uniform) == slug {
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

// toolNameToSlug converts a uniform tool name to a URL-safe slug.
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

// GetDiscoveryWarnings returns warnings from the last discovery run.
// Safe to call concurrently.
func (m *Manager) GetDiscoveryWarnings() []DiscoveryWarning {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]DiscoveryWarning, len(m.discoveryWarnings))
	copy(out, m.discoveryWarnings)
	return out
}
