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

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
	"github.com/hollis-labs/nanite/internal/store"
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
	servers                map[string]MCPTransport // name -> transport
	serverTiers            map[string]TrustTier    // name -> trust tier
	firstPartyBuiltinNames map[string]bool         // name -> true, populated ONLY by AddBuiltinServer (see isFirstPartyBuiltinServerLocked)
	pluginServers          map[string][]string     // pluginID -> server names (reverse map for hot-unload)
	tools                  []*toolEntry            // all discovered tools with server association (pointer slice: entries are mutated in place post-insertion by collision-rename, so a later append reallocating this slice must never orphan an outstanding uniformIndex pointer — see assignUniformNameLocked)
	uniformIndex           map[string]*toolEntry   // uniform name → entry (owns the *toolEntry)
	discoveryWarnings      []DiscoveryWarning      // tools rejected during discovery
	LoadChecker            ToolLoadChecker         // optional loadType filter
	mu                     sync.RWMutex
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
		servers:                make(map[string]MCPTransport),
		serverTiers:            make(map[string]TrustTier),
		firstPartyBuiltinNames: make(map[string]bool),
		pluginServers:          make(map[string][]string),
		uniformIndex:           make(map[string]*toolEntry),
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

// AddBuiltinServer registers one of nanite's own first-party in-process
// builtin servers (self/dev/code/general, and any future addition) at
// TierBuiltin, AND marks name as protected by the reserved-namespace
// defense in assignUniformNameLocked (see isFirstPartyBuiltinServerLocked).
//
// This is THE call cmd/nanite/main.go's real builtin-registration call
// sites must use. Before this method existed, "register a builtin" (an
// AddServer call in main.go) and "protect that builtin's bare tool-name
// slot from eviction" (a case in a hand-maintained switch statement in
// naming.go) were two separate actions a human had to remember to keep in
// sync — the exact bug shape that let a proxied server silently steal
// nanite's own dev_bash tool before commit 5144590. Registering through
// AddBuiltinServer makes them the same action: adding a fifth first-party
// builtin only requires one new call here, nothing else.
//
// Test fixtures that want a server at TierBuiltin WITHOUT first-party
// protection — to exercise plain tier-based collision behavior, per the
// doc comment on isFirstPartyBuiltinServerLocked — must keep calling
// AddServer directly. That path deliberately does NOT populate
// firstPartyBuiltinNames, so a same-named server registered that way is
// never treated as first-party no matter what tier it carries.
//
// Propagates any error from AddServer (empty name, nil transport,
// duplicate registration) without touching firstPartyBuiltinNames.
func (m *Manager) AddBuiltinServer(name string, transport MCPTransport) error {
	if err := m.AddServer(name, transport, TierBuiltin); err != nil {
		return err
	}
	m.mu.Lock()
	m.firstPartyBuiltinNames[name] = true
	m.mu.Unlock()
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
	delete(m.firstPartyBuiltinNames, name)

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

// AddHTTPServerWithHeaders registers an HTTP-based MCP server that requires
// static headers (auth, routing) on every request. The headers map is copied
// inside NewHTTPTransportWithHeaders. Same error contract as AddHTTPServer.
//
// Header values are NOT logged — only their key set — so a Bearer token doesn't
// leak into structured logs. CW-20260501-0005 sub-ticket 2.
func (m *Manager) AddHTTPServerWithHeaders(name, url string, headers map[string]string, tier TrustTier) error {
	if err := m.AddServer(name, NewHTTPTransportWithHeaders(url, headers), tier); err != nil {
		return err
	}
	headerKeys := make([]string, 0, len(headers))
	for k := range headers {
		headerKeys = append(headerKeys, k)
	}
	slog.Info("mcp: server using HTTP transport with headers",
		"name", name, "url", url, "tier", string(tier), "header_keys", headerKeys)
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

// isFirstPartyBuiltinServerLocked reports whether server was registered
// through AddBuiltinServer — i.e. it is one of nanite's own in-process
// builtin servers, not merely a server that happens to carry TierBuiltin.
//
// Deliberately independent of TrustTier: test fixtures and future callers
// legitimately register arbitrary/hostile servers at TierBuiltin via the
// ordinary AddServer path to exercise plain collision behavior, so tier
// alone can't distinguish "one of nanite's real builtins" from "some
// other server that happens to carry that tier." Caller must hold mu.
func (m *Manager) isFirstPartyBuiltinServerLocked(name string) bool {
	return m.firstPartyBuiltinNames[name]
}

// DiscoverTools queries all registered servers for their tools and runs the
// per-tier validator pipeline (S4b T2). Tools failing per-tool validation
// (ValidateToolMeta) are skipped with a DiscoveryWarning. Cross-tool checks
// (ValidateToolSet — high tool count, duplicate names) emit the same
// warnings, but nothing is dropped as a result: an operator decides whether
// an unusually large server is worth keeping connected, Nanite doesn't
// silently truncate it out from under the agent (CW-20260815-0019).
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

		tools, err := transport.ListTools(ctx)
		if err != nil {
			slog.Warn("mcp: failed to discover tools", "server", name, "err", err)
			continue
		}

		// Cross-tool checks: advisory only, nothing is dropped as a result.
		// Each ValidationError → one DiscoveryWarning.
		for _, ve := range ValidateToolSet(tier, tools) {
			slog.Warn("mcp: discovery cross-tool check",
				"server", name, "field", ve.Field, "reason", ve.Reason)
			m.discoveryWarnings = append(m.discoveryWarnings, DiscoveryWarning{
				ServerName: name,
				ToolName:   ve.Value, // empty for high tool count; tool name for dup
				Reason:     ve.Field,
			})
		}

		advertised := len(tools)
		accepted := 0
		renames := 0
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

			if uniform != t.Name {
				renames++
				// Self-server tools that get renamed are unreachable to agents
				// (the agent invokes by bare name). Loud-detect any future
				// regression in the namespace defense.
				if name == SelfServerName {
					slog.Warn("mcp: self-server tool renamed at registration — agents cannot invoke it",
						"tool", t.Name, "uniform", uniform)
					m.discoveryWarnings = append(m.discoveryWarnings, DiscoveryWarning{
						ServerName: name,
						ToolName:   t.Name,
						Reason:     "self_tool_renamed",
					})
				}
			}

			entry := &toolEntry{
				serverName:  name,
				uniformName: uniform,
				tool:        t,
			}
			// Store the pointer itself (not a dereferenced copy re-addressed
			// into the slice) — m.tools appends below can reallocate this
			// slice's backing array at any later iteration, which would
			// silently orphan a `&m.tools[i]`-style address taken earlier.
			// Collision handling in assignUniformNameLocked mutates entries
			// in place via their uniformIndex pointer (e.g.
			// existing.uniformName = incumbentDisambig) well after
			// insertion, so that pointer must stay valid for the entry's
			// entire lifetime, not just until the next reallocation.
			m.tools = append(m.tools, entry)
			m.uniformIndex[uniform] = entry
			accepted++
			totalTools++
		}

		slog.Info("mcp: discovered tools",
			"server", name,
			"tier", string(tier),
			"advertised", advertised,
			"accepted", accepted,
			"renames", renames,
		)
	}

	span.SetAttributes(
		attribute.Int("nanite.mcp.tools.total", totalTools),
		attribute.Int("nanite.mcp.servers.count", len(m.servers)),
	)

	slog.Info("mcp: discovery complete", "total_tools", totalTools, "servers", len(m.servers))
	return nil
}

// GetAllTools returns all enabled tools. Opt-in tools excluded unless enabled.
func (m *Manager) GetAllTools() []llmtypes.ToolDefinition {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.getAllToolsLocked()
}

// getAllToolsLocked returns all enabled tools. Opt-in tools are excluded
// unless the LoadChecker says they are enabled. Caller must hold mu.RLock.
func (m *Manager) getAllToolsLocked() []llmtypes.ToolDefinition {
	defs := make([]llmtypes.ToolDefinition, 0, len(m.tools))
	for _, entry := range m.tools {
		if m.LoadChecker != nil && !m.LoadChecker.IsToolEnabled(entry.uniformName) {
			continue
		}
		defs = append(defs, llmtypes.ToolDefinition{
			Name:        entry.uniformName,
			Description: entry.tool.Description,
			InputSchema: entry.tool.InputSchema,
		})
	}
	return defs
}

// GetAllToolsUnfiltered returns every discovered tool regardless of loadType.
// Used for diagnostics and the tool load preferences UI. Names are uniform.
func (m *Manager) GetAllToolsUnfiltered() []llmtypes.ToolDefinition {
	m.mu.RLock()
	defer m.mu.RUnlock()
	defs := make([]llmtypes.ToolDefinition, 0, len(m.tools))
	for _, entry := range m.tools {
		defs = append(defs, llmtypes.ToolDefinition{
			Name:        entry.uniformName,
			Description: entry.tool.Description,
			InputSchema: entry.tool.InputSchema,
		})
	}
	return defs
}

// LookupToolInputSchema returns the JSON Schema declared for the named
// tool's input, or (nil, false) when no such tool is registered. The name
// is the uniform agent-facing name (post ADR-002 flattening) — the same
// name an LLM would pass to a tool call.
//
// Used by tool_validate (B1, CW-20260429-0006) so the pre-flight
// schema check can reach across every registered server uniformly. The
// returned map is the raw schema and MUST NOT be mutated by callers.
func (m *Manager) LookupToolInputSchema(uniformName string) (map[string]any, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, found := m.uniformIndex[uniformName]
	if !found {
		return nil, false
	}
	return entry.tool.InputSchema, true
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
//  1. Reserved-namespace defense (CW-20260508-0015) — server-scoped. If
//     the newcomer is the self server, it ALWAYS wins the bare slot; any
//     incumbent third-party tool sitting on it is force-prefixed and
//     rewritten in place. If the newcomer is non-self and the bare slot
//     is currently held by the self server, the newcomer is force-
//     prefixed with the server (`<server>_<tool>`).
//  2. Default — bare tool name (`memory_write` from `mux`) when no self
//     ownership applies and the slot is free.
//  3. Collision — if the default slot is already occupied by a tool from
//     a different (non-self) server, fall back to `<server>_<tool>` and
//     try that slot. Both the incumbent and the newcomer keep their
//     disambiguated forms; the bare slot is freed (the incumbent is
//     rewritten too).
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

	// (1a) Reserved-namespace defense — newcomer is a first-party builtin
	// (self/dev/code/general). Builtins ALWAYS own the bare slot for any
	// name they publish. A non-builtin (proxied/third-party) server that
	// registered alphabetically earlier and grabbed the slot gets
	// force-prefixed and rewritten in place.
	if IsReservedSelfToolName(serverName, toolName) || m.isFirstPartyBuiltinServerLocked(serverName) {
		if existing, taken := m.uniformIndex[bare]; taken && existing.serverName != serverName && !m.isFirstPartyBuiltinServerLocked(existing.serverName) {
			incumbentDisambig := DisambiguatedToolName(existing.serverName, existing.tool.Name)
			if _, conflict := m.uniformIndex[incumbentDisambig]; conflict && existing.uniformName != incumbentDisambig {
				// Incumbent's disambiguated slot is already taken by a
				// third tool — refuse to clobber. Returning "" drops the
				// self-tool; DiscoverTools then emits a
				// `uniform_name_collision` discovery warning for it (the
				// generic drop path, not `self_tool_renamed` — that only
				// fires when uniform != t.Name).
				return ""
			}
			delete(m.uniformIndex, bare)
			existing.uniformName = incumbentDisambig
			m.uniformIndex[incumbentDisambig] = existing
			slog.Warn("mcp: first-party builtin claims bare slot — incumbent third-party tool force-prefixed",
				"name", bare,
				"incumbent_server", existing.serverName,
				"incumbent_uniform", incumbentDisambig,
			)
			m.discoveryWarnings = append(m.discoveryWarnings, DiscoveryWarning{
				ServerName: existing.serverName,
				ToolName:   existing.tool.Name,
				Reason:     "uniform_name_collision_disambiguated",
			})
		}
		return bare
	}

	// (1b) Reserved-namespace defense — newcomer is a non-builtin
	// (proxied/third-party) server and the bare slot is held by a
	// first-party builtin. Force-prefix the newcomer; the builtin tool
	// keeps the bare slot it already owns.
	if existing, taken := m.uniformIndex[bare]; taken &&
		(IsReservedSelfToolName(existing.serverName, existing.tool.Name) || m.isFirstPartyBuiltinServerLocked(existing.serverName)) {
		disambig := DisambiguatedToolName(serverName, toolName)
		if _, takenDisambig := m.uniformIndex[disambig]; takenDisambig {
			return ""
		}
		slog.Warn("mcp: third-party tool collides with reserved builtin namespace; force-prefixed",
			"server", serverName, "tool", toolName, "uniform", disambig)
		return disambig
	}

	// (2) Default slot.
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

// AutoDiscover runs tool discovery and diffs the result against the skills
// table's existing auto-discovered rows.
//
// TASKS/skills/01: this used to also create/flag skills rows as a side
// effect (one row per newly visible tool, category="auto-discovered"; a
// "removed":true settings flag for tools that disappeared) — that write
// path is cut in full per docs/engineering/architecture/20-skills.md's
// "Scope: skills are authored packages only" section ("mcp.Manager.
// AutoDiscover's writes into the skills table stop entirely. Tool
// visibility, permissions, and allowlisting remain exactly what they
// already are — a tool-catalog concern... untouched by this redesign.").
// The diffing logic below (Added/Removed/Total, computed against whatever
// auto-discovered rows already exist in the DB from before this cut) is
// otherwise unchanged.
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
	currentTools := make(map[string]*toolEntry, len(m.tools))
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

	// Diff new tools against existing auto-discovered skill rows. TASKS/
	// skills/01 cut the store.Skill{}+CreateSkill write that used to happen
	// here — the slug comparison against existingSlugs still reports newly
	// visible tools via diff.Added, it just no longer persists a row for
	// them.
	for uniform := range currentTools {
		slug := toolNameToSlug(uniform)
		if _, exists := existingSlugs[slug]; exists {
			continue
		}
		diff.Added = append(diff.Added, uniform)
		slog.Info("mcp: auto-discovered new tool", "slug", slug)
	}

	// Diff removed tools against existing auto-discovered skill rows. TASKS/
	// skills/01 cut the "removed":true Settings rewrite + UpdateSkill write
	// that used to happen here — diff.Removed still reports tools that
	// disappeared, it just no longer flags the (now permanently
	// unmaintained) DB row.
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

// RestartStdioTransports closes every stdio MCP subprocess registered on
// the manager. Each transport's start() is lazy, so the next ListTools /
// CallTool against a closed transport reaps the (already-dead) process and
// spawns a fresh subprocess in its place. Non-stdio transports (HTTP,
// plugin, builtin) are skipped — only stdio subprocesses can wedge in a
// way restart-via-respawn fixes.
//
// Idempotent: the underlying *StdioTransport.Close => killAndReapLocked
// is no-op when !started, so repeated calls during a still-restarting
// state are safe.
//
// Bounded by ctx — returns ctx.Err() promptly when the caller's deadline
// fires (e.g. the broker's 10s remediation timeout). Mirrors the locking
// shape of Close: snapshot under lock, release before calling per-
// transport Close to avoid deadlocking concurrent ExecuteTool callers.
//
// Returns nil on success even when zero stdio transports are registered
// (a vacuously-successful restart for non-stdio-only deployments).
func (m *Manager) RestartStdioTransports(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	m.mu.Lock()
	type namedStdio struct {
		name      string
		transport *StdioTransport
	}
	snapshot := make([]namedStdio, 0, len(m.servers))
	for name, t := range m.servers {
		if st, ok := t.(*StdioTransport); ok {
			snapshot = append(snapshot, namedStdio{name: name, transport: st})
		}
	}
	m.mu.Unlock()

	for _, ns := range snapshot {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := ns.transport.Close(); err != nil {
			// Close logs internally; callers care about ctx errors more
			// than per-transport reap failures (the next start() will
			// surface a real spawn failure if the subprocess is broken).
			slog.Warn("mcp: restart stdio transport close error", "server", ns.name, "err", err)
		}
	}
	return nil
}
