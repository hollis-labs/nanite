package toolclient

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/describer"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
)

// MaxSelectedTools is the maximum number of tools returned by SelectTools.
const MaxSelectedTools = 15

// ToolClient mediates all tool access: selection, permissions, and execution.
type ToolClient struct {
	MCPManager *mcp.Manager
	Store      *store.Store
	Config     *Config
	Builtins   *BuiltinToolRegistry

	// registeredTools holds tools registered directly via RegisterTools —
	// the seam callers without a live MCP manager (chiefly tests, and the
	// /api/tools/select preview path) use to inject a synthetic catalog.
	// Guarded by registeredToolsMu; catalogTools() unions this with
	// MCPManager.GetAllTools() to build the full selection candidate set.
	// Phase 0 item 22: replaces the go-toolbroker LocalBroker's tool
	// registry — see decision log §11.
	registeredTools   []llmtypes.ToolDefinition
	registeredToolsMu sync.RWMutex

	// Describers is the opt-in per-call description-render registry
	// (CW-20260512-0105 / SP-20260512-0008 W1B). Tools that need
	// caller-specific descriptions register a Describer keyed by tool
	// name; the materialization site calls RenderDescriptions on the
	// final filtered tool set. Static-description tools (the majority)
	// have no Describer and emit their registration-time description.
	// Lives in the leaf package internal/describer so internal/mcp can
	// author Describers without re-introducing an import cycle through
	// internal/toolclient → internal/mcp → internal/toolclient.
	Describers *describer.Registry

	// DeveloperModeFunc, when non-nil, overrides the default developer_mode
	// lookup (which reads user_settings from Store). Used in tests to inject
	// a known value without a real SQLite database.
	// In production this is nil and developerModeEnabled() falls back to
	// the Store read.
	DeveloperModeFunc func() bool
}

// SelectResult is the return shape of ToolClient.SelectToolsAsProvider. It
// carries the provider-shaped tool definitions for the LLM.
//
// Phase 0 item 22: this used to also carry OverrideBlock, a markdown
// "## Tool Overrides" section composed from per-tool Hints via the
// go-toolbroker enricher (tool_enrichments table). Cut entirely per the
// operator's 2026-08-18 resolution — no port-forward — because the
// enrichment table's write path was already dead (18a-cut-dead-storage-
// and-config), making the read side structurally inert. See decision log
// §11.
type SelectResult struct {
	Tools []llmtypes.ToolDefinition
}

// New creates a new ToolClient.
func New(mcpManager *mcp.Manager, s *store.Store, cfg *Config) *ToolClient {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	return &ToolClient{
		MCPManager: mcpManager,
		Store:      s,
		Config:     cfg,
		Builtins:   NewBuiltinToolRegistry(),
		Describers: describer.NewRegistry(),
	}
}

// RenderDescriptions returns a slice with per-tool descriptions rendered
// for the given caller. Tools without a registered Describer pass through
// unchanged; tools whose Describer returns "" also pass through (fall
// back to the static description).
//
// Aliasing contract: the hot path (Describers registered for one or more
// tools in the input) returns a newly-allocated slice so callers may
// safely retain it without aliasing the input. The fast paths (`tb` is
// nil, `tb.Describers` is nil, or `len(tools) == 0`) return the input
// slice as-is — callers must not assume independence from the input in
// those cases. The input slice is never mutated under any path.
//
// This is the materialization-site hook for the Tool Broker's per-call
// description-render contract (CW-20260512-0105). Call sites: the
// service-layer tool selection (service/tool.go SelectForAgent) after
// allowlist and chat-surface filtering, immediately before the tool set
// is handed to the LLM.
//
// Cacheable-prefix note: per-call descriptions land OUTSIDE the
// cacheable prefix. Tools opting into Describe should be placed at the
// tail of the tool array so the cacheable head (static-description
// tools) is not invalidated per call. The cache-marker priority work
// (CW-20260512-0109 / W3) codifies this placement constraint.
func (tb *ToolClient) RenderDescriptions(ctx context.Context, tools []llmtypes.ToolDefinition, caller describer.CallerAgent) []llmtypes.ToolDefinition {
	if tb == nil || tb.Describers == nil || len(tools) == 0 {
		return tools
	}
	out := make([]llmtypes.ToolDefinition, len(tools))
	copy(out, tools)
	for i := range out {
		d, ok := tb.Describers.Get(out[i].Name)
		if !ok {
			continue
		}
		rendered := d.Describe(ctx, caller)
		if rendered == "" {
			// Describer signaled fall-through — keep static description.
			continue
		}
		out[i].Description = rendered
	}
	return out
}

// IsBuiltinTool reports whether the named tool is a builtin (registered
// via tb.Builtins.RegisterBuiltins). Used to distinguish builtin tools
// from MCP-discovered tools on the uniform agent-facing surface (ADR-002),
// where the legacy `mcp__` prefix is no longer available as a signal.
func (tb *ToolClient) IsBuiltinTool(name string) bool {
	if tb.Builtins == nil {
		return false
	}
	return tb.Builtins.Has(name)
}

// strictTrue is a pointer to true, retained for tools that explicitly opt into
// Anthropic server-side input-schema enforcement. Default for new tools is nil
// (non-strict). See decisions.nanite.tools.strict_default_off in Vanta.
var strictTrue = func() *bool { v := true; return &v }()

// RegisterTools registers tool definitions directly on this ToolClient's
// catalog (see registeredTools). This is the seam tests (and the
// /api/tools/select preview path, when no live MCP manager backs the
// client) use to inject a synthetic tool set. Production MCP-discovered
// tools do NOT flow through this method — they reach catalogTools() via
// MCPManager.GetAllTools() instead.
//
// Phase 0 item 22: replaces the go-toolbroker LocalBroker.RegisterTools
// call this used to make. Same append-only semantics as the library
// method it replaces — callers registering the same tool name twice get
// two entries; catalogTools() dedupes by name at read time.
func (tb *ToolClient) RegisterTools(tools []llmtypes.ToolDefinition) {
	tb.registeredToolsMu.Lock()
	tb.registeredTools = append(tb.registeredTools, tools...)
	tb.registeredToolsMu.Unlock()
	slog.Info("toolclient: registered tools", "count", len(tools))
}

// catalogTools returns the full tool-selection candidate set: tools
// registered directly via RegisterTools, unioned with tools discovered by
// the MCP manager (mcp.Manager.GetAllTools — the real production source).
// Deduped by name; a directly-registered tool wins over a same-named
// MCP-discovered one.
//
// Phase 0 item 22 (decision log §11): replaces go-toolbroker's
// LocalBroker.SelectTools rule-matching pass, which — with
// NaniteDefaultRules' single "*" catch-all rule always installed — always
// returned the entire registered catalog, unranked. Returning the full
// catalog directly here preserves that real production behavior without
// the rule-engine machinery. Real narrowing happens downstream: the
// agent_tools grant filter (service/tool.go's filterToolsByAgentTools —
// replaced the old schema-v2 tools allowlist / filterToolsByAllowlist as of
// TASKS/phase-4/05-wire-select-for-agent-to-read-agent-tools.md, and the
// legacy tool_permissions/CheckPermission mechanism entirely as of
// TASKS/adhoc/02-remove-tool-permissions-collapse-to-agent-tools.md), the
// chat-role surface filter (applyChatSurfaceFilter), the developer_mode
// dev-tool gate, and progressive discovery.
func (tb *ToolClient) catalogTools() []llmtypes.ToolDefinition {
	tb.registeredToolsMu.RLock()
	direct := make([]llmtypes.ToolDefinition, len(tb.registeredTools))
	copy(direct, tb.registeredTools)
	tb.registeredToolsMu.RUnlock()

	seen := make(map[string]bool, len(direct))
	out := make([]llmtypes.ToolDefinition, 0, len(direct))
	for _, t := range direct {
		if seen[t.Name] {
			continue
		}
		seen[t.Name] = true
		out = append(out, t)
	}
	if tb.MCPManager != nil {
		for _, t := range tb.MCPManager.GetAllTools() {
			if seen[t.Name] {
				continue
			}
			seen[t.Name] = true
			out = append(out, t)
		}
	}
	return out
}

// selectToolsUncapped returns the full tool-selection candidate set
// (catalogTools) WITHOUT applying MaxSelectedTools or the token-budget
// prune. Callers that still need to run permission/allowlist filtering on
// the result (SelectToolsAsProvider, and — one layer up — service/tool.go's
// schema-v2 tools allowlist) must defer capping until after that
// filtering, or a correctly-declared, correctly-permitted tool can be
// truncated out before its own allowlist ever sees it (e.g. a
// late-alphabet tool name among Torque's ~90+ registered tools). See
// FinalizeToolSelection for the capping step this defers to.
//
// intent, hints, workspaceID, and agentID are accepted for API-surface
// stability (callers throughout the package pass them; SelectByIntent and
// the reasoning-augmented ranking in ranking.go still use intent/hints for
// scoring) but no longer drive selection here — Phase 0 item 22 retired
// the rule-matching layer that used to key off them (decision log §11).
func (tb *ToolClient) selectToolsUncapped(_ context.Context, _ string, _ []string, _, _ string) (tools []llmtypes.ToolDefinition, total int, err error) {
	tools = tb.catalogTools()
	return tools, len(tools), nil
}

// toolTokenBudget computes the token budget for tool definitions from the
// client's Config and the caller's per-session context window. windowSize
// <= 0 means the model is unknown — falls back to DefaultContextWindowTokens
// so behaviour on unknown models is preserved (never a hard failure).
func (tb *ToolClient) toolTokenBudget(windowSize int) (budget, ctxWindow int) {
	budgetPct := tb.Config.ToolTokenBudgetPct
	if budgetPct <= 0 {
		budgetPct = DefaultToolTokenBudgetPct
	}
	ctxWindow = windowSize
	if ctxWindow <= 0 {
		ctxWindow = tb.Config.ContextWindowTokens
	}
	if ctxWindow <= 0 {
		ctxWindow = DefaultContextWindowTokens
	}
	return int(budgetPct * float64(ctxWindow)), ctxWindow
}

// SelectTools returns tools filtered by intent and hints, capped at
// MaxSelectedTools.
//
// This is the self-contained entry point (used by the /api/tools/select
// preview endpoint and any caller with no further permission/allowlist
// filtering step of its own) — it applies the cap and token-budget prune
// internally. The live agent pipeline (service/tool.go SelectForAgent) goes
// through SelectToolsAsProvider + FinalizeToolSelection instead, so capping
// happens AFTER permission and allowlist filtering (CW-20260815-0011).
//
// windowSize is the per-session context window in tokens (from models.dev /
// user settings). When windowSize <= 0 this falls back to
// DefaultContextWindowTokens so behaviour on unknown models is preserved.
func (tb *ToolClient) SelectTools(ctx context.Context, intent string, hints []string, workspaceID, agentID string, windowSize int) ([]llmtypes.ToolDefinition, error) {
	tools, total, err := tb.selectToolsUncapped(ctx, intent, hints, workspaceID, agentID)
	if err != nil {
		return nil, err
	}

	if len(tools) > MaxSelectedTools {
		tools = tools[:MaxSelectedTools]
	}

	tokenBudget, ctxWindow := tb.toolTokenBudget(windowSize)
	beforeCount := len(tools)
	tools = PruneToolsToTokenBudget(tools, tokenBudget)
	if len(tools) < beforeCount {
		slog.Info("toolclient: pruned tools due to token budget",
			"before", beforeCount, "after", len(tools), "budget", tokenBudget)
	}

	slog.Info("toolclient: selected tools for intent",
		"selected", len(tools), "total", total, "intent", intent,
		"workspace", workspaceID, "agent", agentID,
		"tool_tokens", EstimateToolTokens(tools), "budget", tokenBudget,
		"ctx_window", ctxWindow)

	return tools, nil
}

// DevServerName is the MCP server name for developer tools (dev_bash, dev_read,
// dev_write, dev_edit, dev_glob, dev_grep). Tools from this server are gated
// behind developer_mode — see isDevTool and the gate logic in
// SelectToolsAsProvider / CallTool.
const DevServerName = "dev"

// isDevTool reports whether a tool name belongs to the dev server. With
// MCP internalization (CW-20260427-0017, ADR-002) tool names are
// uniform agent-facing — there is no `mcp__server__` prefix. Dev tools
// are recognized by their `dev_*` prefix; the legacy `mcp__dev__*` form
// is no longer emitted on the agent surface.
func isDevTool(toolName string) bool {
	return strings.HasPrefix(toolName, DevServerName+"_")
}

// developerModeEnabled reports whether developer_mode is active for this
// ToolClient instance.
//
// Resolution order:
//  1. DeveloperModeFunc (non-nil) — used by tests to inject a known value
//     without a real SQLite database.
//  2. Store.GetUserSettings() — production path; reads from user_settings.
//
// Fails closed: returns false on any store error so that non-developer users
// never accidentally gain access to dev tools.
func (tb *ToolClient) developerModeEnabled() bool {
	if tb.DeveloperModeFunc != nil {
		return tb.DeveloperModeFunc()
	}
	if tb.Store == nil {
		return false
	}
	us, err := tb.Store.GetUserSettings(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */)
	if err != nil {
		slog.Warn("toolclient: could not read user_settings for developer_mode check; defaulting to false", "err", err)
		return false
	}
	return us.DeveloperMode
}

// SelectToolsAsProvider returns selected tools converted to
// llmtypes.ToolDefinition format.
//
// Deliberately uncapped (CW-20260815-0011): the MaxSelectedTools cut and
// token-budget prune are NOT applied here. This function no longer applies
// any per-tool permission filtering of its own (TASKS/adhoc/02-remove-
// tool-permissions-collapse-to-agent-tools.md removed the tool_permissions-
// JSON CheckPermission call that used to run here) — the caller
// (service/tool.go SelectForAgent) unconditionally applies the
// agent_tools-authoritative filter (filterToolsByAgentTools) to this
// function's output immediately afterward, so a second, redundant
// per-candidate DB round trip here would add cost without adding
// protection. Capping is deferred to FinalizeToolSelection for the same
// reason it always was: it must run once ALL filtering (agent_tools +
// chat-surface) is done, or a correctly-granted tool could be truncated
// out before its own grant ever got a chance to keep it.
//
// Dev-tool gate: tools from the "dev" server (dev_bash, dev_read, dev_write,
// dev_edit, dev_glob, dev_grep) are stripped from the returned set when
// developer_mode is false in user_settings. This prevents the LLM from ever
// seeing or requesting those tools in non-developer sessions.
func (tb *ToolClient) SelectToolsAsProvider(ctx context.Context, intent string, hints []string, workspaceID, agentID string) (*SelectResult, error) {
	tools, _, err := tb.selectToolsUncapped(ctx, intent, hints, workspaceID, agentID)
	if err != nil {
		return nil, err
	}

	// Read developer_mode once for this selection pass.
	devMode := tb.developerModeEnabled()

	// Start with built-in tools — always available regardless of MCP status.
	// The agent_tools filter the caller applies right after this function
	// returns covers builtins and catalog tools identically, so there is no
	// permission check to duplicate here.
	var defs []llmtypes.ToolDefinition
	if tb.Builtins != nil {
		builtins := tb.Builtins.GetBuiltins()
		defs = make([]llmtypes.ToolDefinition, 0, len(builtins)+len(tools))
		for _, bt := range builtins {
			// Dev-tool gate: skip dev tools when developer_mode is off.
			if !devMode && isDevTool(bt.Name) {
				continue
			}
			defs = append(defs, bt)
		}
	} else {
		defs = make([]llmtypes.ToolDefinition, 0, len(tools))
	}

	// Append the catalog tools (registered directly + MCP-discovered).
	// Names are uniform (no `mcp__server__` prefix) per ADR-002.
	//
	// Strict defaults to nil (non-strict) for all catalog tools. Tools that
	// benefit from Anthropic server-side input-schema enforcement can opt
	// in explicitly by setting Strict to strictTrue (declared above) at
	// registration time. See decisions.nanite.tools.strict_default_off in
	// Vanta for the full rationale: strict was being applied blanket-fashion
	// to all tools, which conflated input-shape validation (where strict
	// adds value) with high-blast-radius permissions (which belong at
	// project/session/agent-profile scope, not at the schema level).
	for _, t := range tools {
		name := t.Name
		// Dev-tool gate: skip dev tools when developer_mode is off.
		if !devMode && isDevTool(name) {
			continue
		}
		defs = append(defs, llmtypes.ToolDefinition{
			Name:        name,
			Description: t.Description,
			InputSchema: t.InputSchema,
			Strict:      nil,
		})
	}

	return &SelectResult{Tools: defs}, nil
}

// CallTool executes a tool call after checking permissions. Routes through
// the MCP Manager via the uniform agent-facing name (no `mcp__server__`
// prefix per ADR-002). The single name is the only signal — it is used
// for permission checks, the dev-tool gate, and the manager lookup.
//
// Dev-tool gate: if the tool name belongs to the "dev" set (dev_bash,
// dev_read, dev_write, dev_edit, dev_glob, dev_grep) and developer_mode
// is false in user_settings, execution is denied regardless of the
// agent's agent_tools grants. This is the execution-time backstop that
// complements the selection-time filter in SelectToolsAsProvider's caller.
//
// Defense-in-depth contract: agent_tools (+ the known_tools.always_included
// escape hatch) is honored at description-render time
// (service/tool.go's filterToolsByAgentTools) so the LLM only sees tools it
// can call. The isToolGrantedToAgent check below is the load-bearing
// execution-time backstop: it is the ONLY gate for callers that invoke
// ToolService.Execute directly without first re-checking
// enforceExecutionRules (chat_reflex_dispatch.go's task_execute dispatch,
// workflow_step_executor.go's tool/LLM steps) — if a tool name slips past
// every selection-time filter (caller bypass, bug, stale tool cache, etc.),
// the gate here denies execution. Do NOT remove this check on the
// assumption a selection-time filter is sufficient.
//
// TASKS/adhoc/02-remove-tool-permissions-collapse-to-agent-tools.md
// replaced the legacy tool_permissions/CheckPermission version of this gate
// with the agent_tools-based isToolGrantedToAgent — same backstop role,
// same load-bearing status, different (now sole-system-of-record) source.
func (tb *ToolClient) CallTool(ctx context.Context, agentID, toolName string, args map[string]any) (string, error) {
	// Dev-tool gate (execution-time backstop). Applied before the permission
	// check so a misconfigured allow-list cannot re-enable dev tools when
	// developer_mode is off.
	if isDevTool(toolName) && !tb.developerModeEnabled() {
		return "", fmt.Errorf("permission denied: tool %q requires developer_mode to be enabled", toolName)
	}

	if !tb.isToolGrantedToAgent(ctx, agentID, toolName) {
		return "", fmt.Errorf("permission denied: tool %q not permitted for agent %q", toolName, agentID)
	}

	if tb.MCPManager == nil {
		return "", fmt.Errorf("no MCP manager configured")
	}

	return tb.MCPManager.ExecuteTool(ctx, toolName, args)
}

// CallToolWithPolicyCheck is a convenience that additionally rejects argument
// shapes matching known escalation patterns (see
// permissions.ArgsContainEscalationPattern) before delegating to CallTool.
// Callers that accept LLM-shaped arguments (e.g., request_tools) should
// prefer this entry point; the base CallTool keeps its existing contract.
func (tb *ToolClient) CallToolWithPolicyCheck(ctx context.Context, agentID, toolName string, args map[string]any) (string, error) {
	if ArgsContainEscalationPattern(args) {
		return "", fmt.Errorf("permission denied: tool %q arguments contain escalation pattern (\"..\")", toolName)
	}
	return tb.CallTool(ctx, agentID, toolName, args)
}

// HandleRequestToolsForAgent wraps HandleRequestTools with per-agent
// permission filtering and argument-level escalation checks. The meta-tool
// itself is name-checked elsewhere; this routine closes the gap where the
// requested inner tool names (and their arguments, when provided) were
// previously returned to the LLM without enforcement.
//
// Behaviour:
//   - If args contain a known escalation pattern (e.g., a path with ".."),
//     return an empty result and a deny summary.
//   - Inner tool names not granted to agentID via agent_tools (+ the
//     known_tools.always_included escape hatch) are dropped from the
//     returned slice; the summary reports denied names.
//
// Policies today do not expose arg-level predicates per tool, so the arg
// check is a conservative global safety net rather than per-tool policy.
func (tb *ToolClient) HandleRequestToolsForAgent(ctx context.Context, agentID string, input map[string]any) ([]llmtypes.ToolDefinition, string) {
	if ArgsContainEscalationPattern(input) {
		return nil, fmt.Sprintf("permission denied: request_tools arguments contain escalation pattern (\"..\") for agent %q", agentID)
	}

	merged, summary := tb.HandleRequestTools(input)
	if len(merged) == 0 {
		return merged, summary
	}

	permitted := make([]llmtypes.ToolDefinition, 0, len(merged))
	var denied []string
	for _, t := range merged {
		if tb.isToolGrantedToAgent(ctx, agentID, t.Name) {
			permitted = append(permitted, t)
			continue
		}
		denied = append(denied, t.Name)
	}

	if len(denied) == 0 {
		return permitted, summary
	}

	if len(permitted) == 0 {
		return permitted, fmt.Sprintf("permission denied: no requested tools permitted for agent %q (denied: %s)", agentID, strings.Join(denied, ", "))
	}

	var names []string
	for _, t := range permitted {
		names = append(names, t.Name)
	}
	return permitted, fmt.Sprintf("Loaded %d tool(s) for agent %q: %s. Denied: %s.",
		len(permitted), agentID, strings.Join(names, ", "), strings.Join(denied, ", "))
}

// isToolGrantedToAgent reports whether toolName is allowed for agentID
// under agent_tools (+ the known_tools.always_included escape hatch) — the
// toolclient-package-local counterpart of
// internal/service/tool.go's filterToolsByAgentTools and
// internal/service/tool_execution_rules.go's
// enforceExecutionRulesViaAgentTools, used by this package's own execution
// (CallTool) and request_tools (HandleRequestToolsForAgent) backstops now
// that TASKS/adhoc/02-remove-tool-permissions-collapse-to-agent-tools.md
// retired tool_permissions/CheckPermission/GetPermissions/PermissionResolver
// entirely.
//
// Nil-safe: tb.Store == nil default-permits, matching CheckPermission's own
// pre-existing "machinery not wired" precedent (chiefly tests). Once a
// Store is wired, a genuinely-ungranted tool is denied unless it carries
// the always_included escape hatch — fail closed, matching
// filterToolsByAgentTools' own default.
func (tb *ToolClient) isToolGrantedToAgent(ctx context.Context, agentID, toolName string) bool {
	if tb.Store == nil {
		return true
	}

	granted, err := tb.Store.ListAgentToolNames(ctx, agentID)
	if err != nil {
		slog.Warn("toolclient: agent_tools lookup failed — denying non-escape-hatch tool",
			"agent", agentID, "tool", toolName, "err", err)
	} else {
		for _, n := range granted {
			if n == toolName {
				return true
			}
		}
	}

	always, err := tb.Store.ListAlwaysIncludedKnownTools(ctx)
	if err != nil {
		slog.Warn("toolclient: list always_included known_tools failed — denying",
			"agent", agentID, "tool", toolName, "err", err)
		return false
	}
	for _, t := range always {
		if t.Name == toolName && t.Status == "available" {
			return true
		}
	}
	return false
}

// ToolSummary is a lightweight tool description without the full schema.
type ToolSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Server      string `json:"server,omitempty"`
}

// ListToolSummaries returns name+description only for all registered tools (no InputSchema).
func (tb *ToolClient) ListToolSummaries() []ToolSummary {
	allTools := tb.ListTools()
	summaries := make([]ToolSummary, 0, len(allTools))
	for _, t := range allTools {
		summaries = append(summaries, ToolSummary{
			Name:        t.Name,
			Description: t.Description,
		})
	}
	return summaries
}

// GetToolsByNames returns full tool definitions for the given names.
func (tb *ToolClient) GetToolsByNames(names []string) []llmtypes.ToolDefinition {
	allTools := tb.ListTools()
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}

	var result []llmtypes.ToolDefinition
	for _, t := range allTools {
		if nameSet[t.Name] {
			result = append(result, t)
		}
	}
	return result
}

// ListTools returns all registered tools as llmtypes.ToolDefinition.
// Built-in tools are always included regardless of MCP manager status.
func (tb *ToolClient) ListTools() []llmtypes.ToolDefinition {
	var all []llmtypes.ToolDefinition

	// Always include built-in tools.
	if tb.Builtins != nil {
		all = append(all, tb.Builtins.GetBuiltins()...)
	}

	// Include MCP-discovered tools.
	if tb.MCPManager != nil {
		all = append(all, tb.MCPManager.GetAllTools()...)
	}

	return all
}

// ListServers returns information about registered MCP servers.
func (tb *ToolClient) ListServers() []mcp.ServerInfo {
	if tb.MCPManager == nil {
		return nil
	}
	return tb.MCPManager.ListServers()
}

// EstimateToolTokens estimates the total token count for a set of tool
// definitions by serializing each to JSON and dividing by 4 (consistent
// with chat.EstimateTokens).
//
// Phase 0 item 22: this used to take []broker.ToolDefinition (the
// go-toolbroker currency type used pre-conversion) while EstimateToolDefTokens
// took the post-conversion []llmtypes.ToolDefinition. With the broker
// retired, both the pre- and post-conversion tool lists share the same
// llmtypes.ToolDefinition type, so this is now a thin alias — kept as a
// separate name because callers throughout this package and its tests
// still reference it at the pre-permission-filter selection stage.
func EstimateToolTokens(tools []llmtypes.ToolDefinition) int {
	return EstimateToolDefTokens(tools)
}

// PruneToolsToTokenBudget removes tools from the end of the slice (lowest
// priority) until the total estimated tokens fits within the given budget.
// At least one tool is always retained.
//
// Phase 0 item 22: thin alias over PruneToolDefsToTokenBudget — see
// EstimateToolTokens's doc comment for why the two currency types merged.
func PruneToolsToTokenBudget(tools []llmtypes.ToolDefinition, budgetTokens int) []llmtypes.ToolDefinition {
	return PruneToolDefsToTokenBudget(tools, budgetTokens)
}

// EstimateToolDefTokens mirrors EstimateToolTokens for llmtypes.ToolDefinition
// (the provider-shaped type used after builtin-prepend + permission
// filtering).
func EstimateToolDefTokens(tools []llmtypes.ToolDefinition) int {
	total := 0
	for _, t := range tools {
		data, err := json.Marshal(t)
		if err != nil {
			n := len(t.Name) + len(t.Description)
			if n == 0 {
				n = 4
			}
			total += n / 4
			continue
		}
		n := len(data) / 4
		if n == 0 {
			n = 1
		}
		total += n
	}
	return total
}

// PruneToolDefsToTokenBudget mirrors PruneToolsToTokenBudget for
// llmtypes.ToolDefinition. At least one tool is always retained.
func PruneToolDefsToTokenBudget(tools []llmtypes.ToolDefinition, budgetTokens int) []llmtypes.ToolDefinition {
	if len(tools) == 0 {
		return tools
	}

	total := EstimateToolDefTokens(tools)
	if total <= budgetTokens {
		return tools
	}

	for len(tools) > 1 && total > budgetTokens {
		last := tools[len(tools)-1]
		data, _ := json.Marshal(last)
		tokens := len(data) / 4
		if tokens == 0 {
			tokens = 1
		}
		total -= tokens
		tools = tools[:len(tools)-1]
	}

	return tools
}

// FinalizeToolSelection applies the MaxSelectedTools cap and token-budget
// prune to a tool list that has ALREADY been through permission and
// roster-membership filtering. This must run LAST in the agent
// tool-selection pipeline (service/tool.go SelectForAgent, after
// filterToolsByAgentTools / applyChatSurfaceFilter — filterToolsByAgentTools
// replaced filterToolsByAllowlist as of
// TASKS/phase-4/05-wire-select-for-agent-to-read-agent-tools.md) —
// applying it earlier let an agent's own
// correctly-declared, correctly-permitted tool be truncated out before its
// own allowlist ever got a chance to keep it (CW-20260815-0011): e.g. a
// late-alphabet tool name among Torque's ~90+ registered tools, sitting
// past index 15 in the broker's unranked candidate order.
//
// windowSize is the per-session context window in tokens; <= 0 falls back
// to DefaultContextWindowTokens, matching SelectTools' behaviour on unknown
// models.
func (tb *ToolClient) FinalizeToolSelection(tools []llmtypes.ToolDefinition, windowSize int) []llmtypes.ToolDefinition {
	if len(tools) > MaxSelectedTools {
		tools = tools[:MaxSelectedTools]
	}

	tokenBudget, _ := tb.toolTokenBudget(windowSize)
	before := len(tools)
	tools = PruneToolDefsToTokenBudget(tools, tokenBudget)
	if len(tools) < before {
		slog.Info("toolclient: pruned tools due to token budget (post-filter)",
			"before", before, "after", len(tools), "budget", tokenBudget)
	}
	return tools
}
