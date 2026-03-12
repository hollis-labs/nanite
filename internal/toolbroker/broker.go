package toolbroker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/hollis-labs/mentat/internal/mcp"
	"github.com/hollis-labs/mentat/internal/provider"
	"github.com/hollis-labs/mentat/internal/store"
	"github.com/hollis-labs/tiamat-tool-broker/broker"
)

// MaxSelectedTools is the maximum number of tools returned by SelectTools.
const MaxSelectedTools = 15

// DefaultFallbackToolCount is the number of tools returned when intent is
// a wildcard or empty — a minimal safe set instead of everything.
const DefaultFallbackToolCount = 5

// ToolBroker mediates all tool access: selection, permissions, and execution.
type ToolBroker struct {
	Broker     *broker.LocalBroker
	MCPManager *mcp.Manager
	Store      *store.Store
	Config     *Config
	Builtins   *BuiltinToolRegistry
}

// New creates a new ToolBroker.
func New(mcpManager *mcp.Manager, s *store.Store, cfg *Config) *ToolBroker {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	lb := broker.NewLocalBroker(nil, cfg.Rules)

	return &ToolBroker{
		Broker:     lb,
		MCPManager: mcpManager,
		Store:      s,
		Config:     cfg,
		Builtins:   NewBuiltinToolRegistry(),
	}
}

// RegisterTools registers tool definitions with the underlying broker.
func (tb *ToolBroker) RegisterTools(tools []broker.ToolDefinition) {
	tb.Broker.RegisterTools(tools)
	log.Printf("toolbroker: registered %d tools", len(tools))
}

// isWildcardIntent returns true if the intent is a wildcard or empty string.
func isWildcardIntent(intent string) bool {
	return intent == "" || intent == "*"
}

// SelectTools returns tools filtered by intent and hints, capped at MaxSelectedTools.
// Optionally scoped by workspace and agent for rule overrides.
// If intent is "*" or empty, logs a warning and returns a minimal fallback set.
func (tb *ToolBroker) SelectTools(ctx context.Context, intent string, hints []string, workspaceID, agentID string) ([]broker.ToolDefinition, error) {
	// Reject wildcard intent — fall back to a minimal safe set.
	if isWildcardIntent(intent) {
		log.Printf("toolbroker: WARNING wildcard/empty intent received (workspace=%s, agent=%s) — returning fallback set of %d tools",
			workspaceID, agentID, DefaultFallbackToolCount)
		intent = "general"
	}

	// Load rules with overrides if scoped.
	if workspaceID != "" || agentID != "" {
		rules := tb.Config.RulesFor(workspaceID, agentID)
		tb.Broker.LoadRules(rules)
	}

	result, err := tb.Broker.SelectTools(ctx, intent, hints)
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
		log.Printf("toolbroker: pruned %d tools to %d due to token budget (%d tokens)",
			beforeCount, len(tools), tokenBudget)
	}

	log.Printf("toolbroker: selected %d/%d tools for intent %q (workspace=%s, agent=%s, tool_tokens=%d, budget=%d)",
		len(tools), result.Total, intent, workspaceID, agentID, EstimateToolTokens(tools), tokenBudget)

	return tools, nil
}

// SelectToolsAsProvider returns selected tools converted to provider.ToolDefinition format.
// Built-in tools are always prepended and do not count against selection limits.
func (tb *ToolBroker) SelectToolsAsProvider(ctx context.Context, intent string, hints []string, workspaceID, agentID string) ([]provider.ToolDefinition, error) {
	tools, err := tb.SelectTools(ctx, intent, hints, workspaceID, agentID)
	if err != nil {
		return nil, err
	}

	// Start with built-in tools — always available regardless of MCP status.
	var defs []provider.ToolDefinition
	if tb.Builtins != nil {
		builtins := tb.Builtins.GetBuiltins()
		defs = make([]provider.ToolDefinition, 0, len(builtins)+len(tools))
		defs = append(defs, builtins...)
	} else {
		defs = make([]provider.ToolDefinition, 0, len(tools))
	}

	// Append broker-selected MCP tools.
	for _, t := range tools {
		name := t.Name
		if t.Server != "" {
			name = fmt.Sprintf("mcp__%s__%s", t.Server, t.Name)
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
// Built-in tools (no mcp__ prefix) are routed to the "self" MCP server automatically.
func (tb *ToolBroker) CallTool(ctx context.Context, agentID, toolName string, args map[string]any) (string, error) {
	// Check permissions.
	if !tb.CheckPermission(agentID, toolName) {
		return "", fmt.Errorf("tool %q denied for agent %q", toolName, agentID)
	}

	if tb.MCPManager == nil {
		return "", fmt.Errorf("no MCP manager configured")
	}

	// Built-in tools don't have the mcp__ prefix. Route them to the "self" server.
	execName := toolName
	if !strings.HasPrefix(toolName, "mcp__") {
		if tb.Builtins != nil {
			for _, b := range tb.Builtins.GetBuiltins() {
				if b.Name == toolName {
					execName = fmt.Sprintf("mcp__self__%s", toolName)
					break
				}
			}
		}
	}

	return tb.MCPManager.ExecuteTool(ctx, execName, args)
}

// GetPermissions loads tool permissions for an agent from the store.
func (tb *ToolBroker) GetPermissions(agentID string) ToolPermissions {
	if tb.Store == nil {
		return ToolPermissions{MaxCallsPerTurn: DefaultMaxCallsPerTurn}
	}

	agent, err := tb.Store.GetAgent(agentID)
	if err != nil {
		log.Printf("toolbroker: could not load agent %s for permissions: %v", agentID, err)
		return ToolPermissions{MaxCallsPerTurn: DefaultMaxCallsPerTurn}
	}

	return ParsePermissions(agent.ToolPermissions)
}

// CheckPermission returns true if the agent is allowed to use the named tool.
func (tb *ToolBroker) CheckPermission(agentID, toolName string) bool {
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
func (tb *ToolBroker) ListToolSummaries() []ToolSummary {
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
func (tb *ToolBroker) GetToolsByNames(names []string) []provider.ToolDefinition {
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
func (tb *ToolBroker) ListTools() []provider.ToolDefinition {
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
func (tb *ToolBroker) ListServers() []mcp.ServerInfo {
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
