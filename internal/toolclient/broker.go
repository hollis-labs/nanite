package toolclient

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-toolbroker/broker"
	"github.com/hollis-labs/nanite/internal/describer"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
)

// MaxSelectedTools is the maximum number of tools returned by SelectTools.
const MaxSelectedTools = 15

// DefaultFallbackToolCount is the number of tools returned when intent is
// a wildcard or empty — a minimal safe set instead of everything.
const DefaultFallbackToolCount = 5

// PermissionResolver resolves an agent's ToolPermissions outside of the
// database. It is intended for file-based agents (synthetic ID prefix
// "file-"), which have no agent_profiles row by design — their definitions
// live on disk. Return ok=false to defer to the store-backed lookup.
//
// Wired by the service layer once the AgentService knows about file
// definitions; tests typically leave it nil and rely on default-permit.
type PermissionResolver func(agentID string) (ToolPermissions, bool)

// ToolClient mediates all tool access: selection, permissions, and execution.
type ToolClient struct {
	LocalBroker        *broker.LocalBroker
	MCPManager         *mcp.Manager
	Store              *store.Store
	Config             *Config
	Builtins           *BuiltinToolRegistry
	PermissionResolver PermissionResolver

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

	// Phase 5 / D3 (CW-20260419-0011) — reasoning-augmented selection.
	// Both fields are nil-safe: when unset, the broker behaves exactly as
	// before (keyword + token budget). Wiring code (service container) sets
	// them when the underlying capabilities are available.
	skills         []ToolPreferenceSkill
	memoryRecaller MemoryRecaller
}

// SelectResult is the return shape of ToolClient.SelectToolsAsProvider. It
// carries both the provider-shaped tool definitions for the LLM and the
// markdown override block composed from per-tool Hints (via the broker's
// WithEnricher option), ready to append to the system prompt.
type SelectResult struct {
	Tools         []llmtypes.ToolDefinition
	OverrideBlock string
}

// New creates a new ToolClient. When s is non-nil, a storeEnricher is wired
// so SelectToolsAsProvider returns per-tool override blocks composed from
// the tool_enrichments table.
func New(mcpManager *mcp.Manager, s *store.Store, cfg *Config) *ToolClient {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	enr := NewStoreEnricher(s)
	lb := broker.NewLocalBroker(nil, cfg.Rules, broker.WithEnricher(enr))

	return &ToolClient{
		LocalBroker: lb,
		MCPManager:  mcpManager,
		Store:       s,
		Config:      cfg,
		Builtins:    NewBuiltinToolRegistry(),
		Describers:  describer.NewRegistry(),
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

// SetSkills attaches operator-authored tool-preference skills to the
// broker. Subsequent SelectToolsAugmented calls factor the skill set into
// ranking. Pass nil to clear.
func (tb *ToolClient) SetSkills(skills []ToolPreferenceSkill) {
	tb.skills = skills
	slog.Info("toolclient: tool-preference skills attached", "count", len(skills))
}

// SetMemoryRecaller attaches a MemoryRecaller used to query Vanta for prior
// successful tool sequences on similar intents. Nil-safe — when unset, the
// memory ranking signal is effectively empty and selection falls back to
// keyword + skills.
func (tb *ToolClient) SetMemoryRecaller(r MemoryRecaller) {
	tb.memoryRecaller = r
	if r != nil {
		slog.Info("toolclient: memory recaller attached")
	}
}

// Skills returns the currently-attached operator skills (read-only — the
// returned slice is the live reference; callers should not mutate it).
func (tb *ToolClient) Skills() []ToolPreferenceSkill { return tb.skills }

// MemoryRecaller returns the attached memory recaller, or nil when none.
func (tb *ToolClient) MemoryRecaller() MemoryRecaller { return tb.memoryRecaller }

// SelectToolsAugmented is the reasoning-augmented selection entry point used
// by the service layer. It wraps SelectWithSignals: gathers the per-call
// memory hits (via the attached MemoryRecaller, if any), and passes the
// attached skills + Config-driven err-toward-more pad through.
//
// On any error from the underlying broker pass the call returns the error;
// errors from the memory recaller are NOT propagated — they're absorbed
// (logged) so memory unreachability never gates tool selection.
func (tb *ToolClient) SelectToolsAugmented(
	ctx context.Context,
	intent string,
	hints []string,
	workspaceID, agentID string,
	windowSize int,
) ([]broker.ToolDefinition, string, string, error) {
	var memHits []ToolPatternHit
	if tb.memoryRecaller != nil {
		hits, err := tb.memoryRecaller.RecallToolPatterns(ctx, intent)
		if err != nil {
			slog.Warn("toolclient: memory recall errored — continuing without memory signal",
				"intent", intent, "err", err)
		} else {
			memHits = hits
		}
	}

	pad := DefaultErrTowardMorePad
	if tb.Config != nil && tb.Config.ErrTowardMorePad >= 0 {
		pad = tb.Config.ErrTowardMorePad
	}

	return tb.SelectWithSignals(ctx, intent, hints, workspaceID, agentID, windowSize, tb.skills, memHits, pad)
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

// RegisterTools registers tool definitions with the underlying broker.
func (tb *ToolClient) RegisterTools(tools []broker.ToolDefinition) {
	tb.LocalBroker.RegisterTools(tools)
	slog.Info("toolclient: registered tools", "count", len(tools))
}

// isWildcardIntent returns true if the intent is a wildcard or empty string.
func isWildcardIntent(intent string) bool {
	return intent == "" || intent == "*"
}

// SelectTools returns tools filtered by intent and hints, capped at MaxSelectedTools,
// together with the per-tool override block from the broker enricher.
// Optionally scoped by workspace and agent for rule overrides.
// If intent is "*" or empty, logs a warning and returns a minimal fallback set.
//
// windowSize is the per-session context window in tokens (from models.dev /
// user settings). When windowSize <= 0 the broker falls back to
// DefaultContextWindowTokens so behaviour on unknown models is preserved.
func (tb *ToolClient) SelectTools(ctx context.Context, intent string, hints []string, workspaceID, agentID string, windowSize int) ([]broker.ToolDefinition, string, error) {
	// Reject wildcard intent — fall back to a minimal safe set.
	if isWildcardIntent(intent) {
		slog.Warn("toolclient: wildcard/empty intent received — returning fallback set",
			"workspace", workspaceID, "agent", agentID, "count", DefaultFallbackToolCount)
		intent = "general"
	}

	// Load rules with overrides if scoped.
	if workspaceID != "" || agentID != "" {
		rules := tb.Config.RulesFor(workspaceID, agentID)
		tb.LocalBroker.LoadRules(rules)
	}

	result, err := tb.LocalBroker.SelectTools(ctx, intent, hints)
	if err != nil {
		return nil, "", fmt.Errorf("select tools: %w", err)
	}

	tools := result.Tools
	if len(tools) > MaxSelectedTools {
		tools = tools[:MaxSelectedTools]
	}

	// Apply token budget pruning using the per-session context window.
	// windowSize <= 0 means the model is unknown — fall back to the static
	// default so behaviour on unknown models is preserved (never a hard failure).
	budgetPct := tb.Config.ToolTokenBudgetPct
	if budgetPct <= 0 {
		budgetPct = DefaultToolTokenBudgetPct
	}
	ctxWindow := windowSize
	if ctxWindow <= 0 {
		ctxWindow = tb.Config.ContextWindowTokens
	}
	if ctxWindow <= 0 {
		ctxWindow = DefaultContextWindowTokens
	}
	tokenBudget := int(budgetPct * float64(ctxWindow))

	beforeCount := len(tools)
	tools = PruneToolsToTokenBudget(tools, tokenBudget)
	if len(tools) < beforeCount {
		slog.Info("toolclient: pruned tools due to token budget",
			"before", beforeCount, "after", len(tools), "budget", tokenBudget)
	}

	slog.Info("toolclient: selected tools for intent",
		"selected", len(tools), "total", result.Total, "intent", intent,
		"workspace", workspaceID, "agent", agentID,
		"tool_tokens", EstimateToolTokens(tools), "budget", tokenBudget,
		"ctx_window", ctxWindow)

	return tools, result.OverrideBlock, nil
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
	us, err := tb.Store.GetUserSettings()
	if err != nil {
		slog.Warn("toolclient: could not read user_settings for developer_mode check; defaulting to false", "err", err)
		return false
	}
	return us.DeveloperMode
}

// SelectToolsAsProvider returns selected tools converted to llmtypes.ToolDefinition format,
// together with the per-turn override block composed from per-tool Hints for
// the FINAL tool set (post permission filtering). Built-in tools are always
// prepended and do not count against selection limits. Enrichment compose
// runs after permission filtering so the override block never mentions a
// tool the LLM won't actually see.
//
// windowSize is the per-session context window in tokens (from models.dev /
// user settings). Pass 0 when the model is unknown — SelectTools will fall
// back to DefaultContextWindowTokens so behaviour is preserved.
//
// Dev-tool gate: tools from the "dev" server (dev_bash, dev_read, dev_write,
// dev_edit, dev_glob, dev_grep) are stripped from the returned set when
// developer_mode is false in user_settings. This prevents the LLM from ever
// seeing or requesting those tools in non-developer sessions.
func (tb *ToolClient) SelectToolsAsProvider(ctx context.Context, intent string, hints []string, workspaceID, agentID string, windowSize int) (*SelectResult, error) {
	tools, overrideBlock, err := tb.SelectTools(ctx, intent, hints, workspaceID, agentID, windowSize)
	if err != nil {
		return nil, err
	}

	// Read developer_mode once for this selection pass.
	devMode := tb.developerModeEnabled()

	// Start with built-in tools — always available regardless of MCP status.
	// Builtins must pass the same permission check as MCP tools; a blanket
	// prepend would bypass deny/allow lists for sensitive builtins (e.g.,
	// dev_bash, dev_write) and let the LLM call them before the execution-
	// time check in CallTool denies them.
	var defs []llmtypes.ToolDefinition
	if tb.Builtins != nil {
		builtins := tb.Builtins.GetBuiltins()
		defs = make([]llmtypes.ToolDefinition, 0, len(builtins)+len(tools))
		for _, bt := range builtins {
			// Dev-tool gate: skip dev tools when developer_mode is off.
			if !devMode && isDevTool(bt.Name) {
				continue
			}
			if !tb.CheckPermission(agentID, bt.Name) {
				continue
			}
			defs = append(defs, bt)
		}
	} else {
		defs = make([]llmtypes.ToolDefinition, 0, len(tools))
	}

	// Append broker-selected MCP tools, filtered by agent permissions.
	// Names are uniform (no `mcp__server__` prefix) per ADR-002; the
	// broker is registered with uniform names by mcp.Manager, so t.Name
	// here is already the agent-facing name.
	//
	// Strict defaults to nil (non-strict) for all broker-registered tools.
	// Tools that benefit from Anthropic server-side input-schema enforcement
	// can opt in explicitly by setting Strict to strictTrue (declared above)
	// at registration time. See decisions.nanite.tools.strict_default_off in
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
		if !tb.CheckPermission(agentID, name) {
			continue
		}
		defs = append(defs, llmtypes.ToolDefinition{
			Name:        name,
			Description: t.Description,
			InputSchema: t.InputSchema,
			Strict:      nil,
		})
	}

	// overrideBlock was returned by SelectTools (from the broker's SelectResult
	// composed via the WithEnricher option). No second LocalBroker.SelectTools
	// call needed — the D1 redundant-call pattern is eliminated here.
	return &SelectResult{Tools: defs, OverrideBlock: overrideBlock}, nil
}

// CallTool executes a tool call after checking permissions. Routes through
// the MCP Manager via the uniform agent-facing name (no `mcp__server__`
// prefix per ADR-002). The single name is the only signal — it is used
// for permission checks, the dev-tool gate, and the manager lookup.
//
// Dev-tool gate: if the tool name belongs to the "dev" set (dev_bash,
// dev_read, dev_write, dev_edit, dev_glob, dev_grep) and developer_mode
// is false in user_settings, execution is denied regardless of the
// agent's permission policy. This is the execution-time backstop that
// complements the selection-time filter in SelectToolsAsProvider.
func (tb *ToolClient) CallTool(ctx context.Context, agentID, toolName string, args map[string]any) (string, error) {
	// Dev-tool gate (execution-time backstop). Applied before the permission
	// check so a misconfigured allow-list cannot re-enable dev tools when
	// developer_mode is off.
	if isDevTool(toolName) && !tb.developerModeEnabled() {
		return "", fmt.Errorf("permission denied: tool %q requires developer_mode to be enabled", toolName)
	}

	if !tb.CheckPermission(agentID, toolName) {
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
//   - Inner tool names that fail CheckPermission for agentID are dropped
//     from the returned slice; the summary reports denied names.
//
// Policies today do not expose arg-level predicates per tool, so the arg
// check is a conservative global safety net rather than per-tool policy.
func (tb *ToolClient) HandleRequestToolsForAgent(agentID string, input map[string]any) ([]llmtypes.ToolDefinition, string) {
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
		if tb.CheckPermission(agentID, t.Name) {
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

// GetPermissions loads tool permissions for an agent. File-based agents
// (ID prefix "file-") are resolved through PermissionResolver when wired —
// they have no agent_profiles row by design, so a store miss is expected.
// DB-backed agent IDs fall through to the store; a miss there is a real
// signal (stale binding or deleted profile) and is logged at WARN.
//
// Only sql.ErrNoRows for file-based IDs is downgraded to DEBUG — a real DB
// error (busy, corruption, I/O) stays at WARN for every agent ID so operational
// issues remain visible.
func (tb *ToolClient) GetPermissions(agentID string) ToolPermissions {
	if tb.PermissionResolver != nil {
		if perms, ok := tb.PermissionResolver(agentID); ok {
			return perms
		}
	}

	fileBased := strings.HasPrefix(agentID, "file-")

	if tb.Store == nil {
		return ToolPermissions{MaxCallsPerTurn: DefaultMaxCallsPerTurn}
	}

	agent, err := tb.Store.GetAgent(agentID)
	if err != nil {
		if fileBased && errors.Is(err, sql.ErrNoRows) {
			slog.Debug("toolclient: file-based agent not in store; using default-permit",
				"agent", agentID, "err", err)
		} else {
			slog.Warn("toolclient: could not load agent for permissions",
				"agent", agentID, "err", err)
		}
		return ToolPermissions{MaxCallsPerTurn: DefaultMaxCallsPerTurn}
	}

	return ParsePermissions(agent.ToolPermissions)
}

// CheckPermission returns true if the agent is allowed to use the named tool.
func (tb *ToolClient) CheckPermission(agentID, toolName string) bool {
	perms := tb.GetPermissions(agentID)
	return perms.CheckPermission(toolName)
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

// EstimateToolTokens estimates the total token count for a set of tool definitions
// by serializing each to JSON and dividing by 4 (consistent with chat.EstimateTokens).
func EstimateToolTokens(tools []broker.ToolDefinition) int {
	total := 0
	for _, t := range tools {
		data, err := json.Marshal(t)
		if err != nil {
			// Fallback: estimate from name + description length.
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

// PruneToolsToTokenBudget removes tools from the end of the slice (lowest priority)
// until the total estimated tokens fits within the given budget.
// At least one tool is always retained.
func PruneToolsToTokenBudget(tools []broker.ToolDefinition, budgetTokens int) []broker.ToolDefinition {
	if len(tools) == 0 {
		return tools
	}

	total := EstimateToolTokens(tools)
	if total <= budgetTokens {
		return tools
	}

	// Remove from end until under budget, keeping at least 1.
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
