package toolclient

import (
	"context"
	"encoding/json"
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
}

// New creates a new ToolClient.
func New(mcpManager *mcp.Manager, s *store.Store, cfg *Config) *ToolClient {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	lb := broker.NewLocalBroker(nil, cfg.Rules)

	return &ToolClient{
		LocalBroker: lb,
		MCPManager:  mcpManager,
		Store:       s,
		Config:      cfg,
		Builtins:    NewBuiltinToolRegistry(),
	}
}

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

// SelectToolsAsProvider returns selected tools converted to provider.ToolDefinition format.
// Built-in tools are always prepended and do not count against selection limits.
func (tb *ToolClient) SelectToolsAsProvider(ctx context.Context, intent string, hints []string, workspaceID, agentID string) ([]provider.ToolDefinition, error) {
	tools, err := tb.SelectTools(ctx, intent, hints, workspaceID, agentID)
	if err != nil {
		return nil, err
	}

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
			if !tb.CheckPermission(agentID, bt.Name) {
				continue
			}
			defs = append(defs, bt)
		}
	} else {
		defs = make([]provider.ToolDefinition, 0, len(tools))
	}

	// Append broker-selected MCP tools, filtered by agent permissions.
	for _, t := range tools {
		name := t.Name
		if t.Server != "" {
			name = fmt.Sprintf("mcp__%s__%s", t.Server, t.Name)
		}
		if !tb.CheckPermission(agentID, name) {
			continue
		}
		defs = append(defs, provider.ToolDefinition{
			Name:        name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	return defs, nil
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
func (tb *ToolClient) CallTool(ctx context.Context, agentID, toolName string, args map[string]any) (string, error) {
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
		if fileBased {
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
