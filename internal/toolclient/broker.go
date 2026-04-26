package toolclient

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/go-toolbroker/broker"
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

	// DeveloperModeFunc, when non-nil, overrides the default developer_mode
	// lookup (which reads user_settings from Store). Used in tests to inject
	// a known value without a real SQLite database.
	// In production this is nil and developerModeEnabled() falls back to
	// the Store read.
	DeveloperModeFunc func() bool
}

// SelectResult is the return shape of ToolClient.SelectToolsAsProvider. It
// carries both the provider-shaped tool definitions for the LLM and the
// markdown override block composed from per-tool Hints (via the broker's
// WithEnricher option), ready to append to the system prompt.
type SelectResult struct {
	Tools         []provider.ToolDefinition
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
	}
}

// strictTrue is a pointer to true used as the default Strict value for
// broker-registered tools. Strict mode causes Anthropic to validate tool
// inputs against the declared schema at call time, surfacing malformed calls
// early rather than wasting retry turns.
//
// Opt-out: pass a *bool pointing to false in provider.ToolDefinition.Strict
// when registering a builtin that is intentionally schema-loose (rare).
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

// SelectTools returns tools filtered by intent and hints, capped at MaxSelectedTools.
// Optionally scoped by workspace and agent for rule overrides.
// If intent is "*" or empty, logs a warning and returns a minimal fallback set.
func (tb *ToolClient) SelectTools(ctx context.Context, intent string, hints []string, workspaceID, agentID string) ([]broker.ToolDefinition, error) {
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
		return nil, fmt.Errorf("select tools: %w", err)
	}

	tools := result.Tools
	if len(tools) > MaxSelectedTools {
		tools = tools[:MaxSelectedTools]
	}

	// Apply token budget pruning.
	budgetPct := tb.Config.ToolTokenBudgetPct
	if budgetPct <= 0 {
		budgetPct = DefaultToolTokenBudgetPct
	}
	ctxWindow := tb.Config.ContextWindowTokens
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
		"tool_tokens", EstimateToolTokens(tools), "budget", tokenBudget)

	return tools, nil
}

// DevServerName is the MCP server name for developer tools (dev_bash, dev_read,
// dev_write, dev_edit, dev_glob, dev_grep). Tools from this server are gated
// behind developer_mode — see isDevTool and the gate logic in
// SelectToolsAsProvider / CallTool.
const DevServerName = "dev"

// isDevTool reports whether a tool name belongs to the dev server. It matches
// both the bare form ("dev_bash") and the MCP-prefixed form
// ("mcp__dev__dev_bash"). This is the canonical check used at both
// selection-time and execution-time to enforce the developer_mode gate.
func isDevTool(toolName string) bool {
	// Prefixed form: mcp__dev__*
	if strings.HasPrefix(toolName, "mcp__"+DevServerName+"__") {
		return true
	}
	// Bare form: dev_* (tools resolved without the mcp__ prefix by the builtin
	// registry or when the LLM omits the prefix).
	if strings.HasPrefix(toolName, DevServerName+"_") {
		return true
	}
	return false
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

// SelectToolsAsProvider returns selected tools converted to provider.ToolDefinition format,
// together with the per-turn override block composed from per-tool Hints for
// the FINAL tool set (post permission filtering). Built-in tools are always
// prepended and do not count against selection limits. Enrichment compose
// runs after permission filtering so the override block never mentions a
// tool the LLM won't actually see.
//
// Dev-tool gate: tools from the "dev" server (dev_bash, dev_read, dev_write,
// dev_edit, dev_glob, dev_grep) are stripped from the returned set when
// developer_mode is false in user_settings. This prevents the LLM from ever
// seeing or requesting those tools in non-developer sessions.
func (tb *ToolClient) SelectToolsAsProvider(ctx context.Context, intent string, hints []string, workspaceID, agentID string) (*SelectResult, error) {
	tools, err := tb.SelectTools(ctx, intent, hints, workspaceID, agentID)
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
	var defs []provider.ToolDefinition
	if tb.Builtins != nil {
		builtins := tb.Builtins.GetBuiltins()
		defs = make([]provider.ToolDefinition, 0, len(builtins)+len(tools))
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
		defs = make([]provider.ToolDefinition, 0, len(tools))
	}

	// Append broker-selected MCP tools, filtered by agent permissions.
	// Strict defaults to true for all broker-registered tools so malformed
	// tool calls fail at the provider boundary instead of wasting retry turns.
	// Tools that require a permissive schema (rare) can opt out by setting
	// Strict: pointer-to-false in their ToolDefinition before registration.
	for _, t := range tools {
		name := t.Name
		if t.Server != "" {
			name = fmt.Sprintf("mcp__%s__%s", t.Server, t.Name)
		}
		// Dev-tool gate: skip dev tools when developer_mode is off.
		if !devMode && isDevTool(name) {
			continue
		}
		if !tb.CheckPermission(agentID, name) {
			continue
		}
		defs = append(defs, provider.ToolDefinition{
			Name:        name,
			Description: t.Description,
			InputSchema: t.InputSchema,
			Strict:      strictTrue, // default-on; nil in ToolDefinition also means strict
		})
	}

	// Obtain the override block from the broker's SelectResult. The broker
	// composes it automatically when an enricher is wired via WithEnricher;
	// we call SelectTools again (cheap in-process call on the already-loaded
	// rule set) purely to retrieve the OverrideBlock. Apply the same wildcard
	// normalisation that SelectTools uses so the intent matches rules identically.
	// Enrichment is cosmetic — an error there never fails selection (the broker
	// swallows it via slog).
	brokerIntent := intent
	if isWildcardIntent(brokerIntent) {
		brokerIntent = "general"
	}
	var overrideBlock string
	if brokerResult, err := tb.LocalBroker.SelectTools(ctx, brokerIntent, hints); err == nil {
		overrideBlock = brokerResult.OverrideBlock
	}

	return &SelectResult{Tools: defs, OverrideBlock: overrideBlock}, nil
}

// CallTool executes a tool call after checking permissions. Routes through the MCP Manager.
// Tools with the mcp__ prefix are routed directly. Unprefixed tools (builtins, native tools)
// are resolved to their owning server via the Manager's tool registry.
//
// Permission enforcement runs twice: once against the caller-supplied name
// and, for unprefixed tools, once more against the resolved mcp__server__tool
// name. Policies written as "mcp__server__*" patterns would otherwise miss
// the bare-name fallback path that resolves via MCPManager.ResolveToolServer
// — an agent with deny_list: ["mcp__dev__*"] could still invoke "dev_bash"
// by omitting the prefix. Structured deny errors are returned for both.
//
// Dev-tool gate: if the tool resolves to the "dev" server (dev_bash, dev_read,
// dev_write, dev_edit, dev_glob, dev_grep) and developer_mode is false in
// user_settings, execution is denied regardless of the agent's permission
// policy. This is the execution-time backstop that complements the
// selection-time filter in SelectToolsAsProvider.
func (tb *ToolClient) CallTool(ctx context.Context, agentID, toolName string, args map[string]any) (string, error) {
	// Dev-tool gate (execution-time backstop). Applied before the permission
	// check so a misconfigured allow-list cannot re-enable dev tools when
	// developer_mode is off.
	if isDevTool(toolName) && !tb.developerModeEnabled() {
		return "", fmt.Errorf("permission denied: tool %q requires developer_mode to be enabled", toolName)
	}

	// Check permissions against the caller-supplied name first.
	if !tb.CheckPermission(agentID, toolName) {
		return "", fmt.Errorf("permission denied: tool %q not permitted for agent %q", toolName, agentID)
	}

	if tb.MCPManager == nil {
		return "", fmt.Errorf("no MCP manager configured")
	}

	// Tools with mcp__ prefix already have routing info — pass through.
	// Unprefixed tools (native/builtin) need server resolution; re-check the
	// resolved prefixed name so deny patterns targeting "mcp__server__*" catch
	// the bare-name bypass route.
	execName := toolName
	if !strings.HasPrefix(toolName, "mcp__") {
		if server, prefixed := tb.MCPManager.ResolveToolServer(toolName); server != "" {
			execName = prefixed
			if !tb.CheckPermission(agentID, execName) {
				return "", fmt.Errorf("permission denied: tool %q (resolved to %q) not permitted for agent %q", toolName, execName, agentID)
			}
		} else {
			return "", fmt.Errorf("tool %q not found in any registered server", toolName)
		}
	}

	return tb.MCPManager.ExecuteTool(ctx, execName, args)
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
func (tb *ToolClient) HandleRequestToolsForAgent(agentID string, input map[string]any) ([]provider.ToolDefinition, string) {
	if ArgsContainEscalationPattern(input) {
		return nil, fmt.Sprintf("permission denied: request_tools arguments contain escalation pattern (\"..\") for agent %q", agentID)
	}

	merged, summary := tb.HandleRequestTools(input)
	if len(merged) == 0 {
		return merged, summary
	}

	permitted := make([]provider.ToolDefinition, 0, len(merged))
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
func (tb *ToolClient) GetToolsByNames(names []string) []provider.ToolDefinition {
	allTools := tb.ListTools()
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}

	var result []provider.ToolDefinition
	for _, t := range allTools {
		if nameSet[t.Name] {
			result = append(result, t)
		}
	}
	return result
}

// ListTools returns all registered tools as provider.ToolDefinition.
// Built-in tools are always included regardless of MCP manager status.
func (tb *ToolClient) ListTools() []provider.ToolDefinition {
	var all []provider.ToolDefinition

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
