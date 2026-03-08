package toolbroker

import (
	"context"
	"fmt"
	"log"

	"github.com/hollis-labs/mentat-chat/internal/mcp"
	"github.com/hollis-labs/mentat-chat/internal/provider"
	"github.com/hollis-labs/mentat-chat/internal/store"
	"github.com/hollis-labs/tiamat-tool-broker/broker"
)

// MaxSelectedTools is the maximum number of tools returned by SelectTools.
const MaxSelectedTools = 15

// ToolBroker mediates all tool access: selection, permissions, and execution.
type ToolBroker struct {
	Broker     *broker.LocalBroker
	MCPManager *mcp.Manager
	Store      *store.Store
	Config     *Config
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
	}
}

// RegisterTools registers tool definitions with the underlying broker.
func (tb *ToolBroker) RegisterTools(tools []broker.ToolDefinition) {
	tb.Broker.RegisterTools(tools)
	log.Printf("toolbroker: registered %d tools", len(tools))
}

// SelectTools returns tools filtered by intent and hints, capped at MaxSelectedTools.
// Optionally scoped by workspace and agent for rule overrides.
func (tb *ToolBroker) SelectTools(ctx context.Context, intent string, hints []string, workspaceID, agentID string) ([]broker.ToolDefinition, error) {
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

	log.Printf("toolbroker: selected %d/%d tools for intent %q (workspace=%s, agent=%s)",
		len(tools), result.Total, intent, workspaceID, agentID)

	return tools, nil
}

// SelectToolsAsProvider returns selected tools converted to provider.ToolDefinition format.
func (tb *ToolBroker) SelectToolsAsProvider(ctx context.Context, intent string, hints []string, workspaceID, agentID string) ([]provider.ToolDefinition, error) {
	tools, err := tb.SelectTools(ctx, intent, hints, workspaceID, agentID)
	if err != nil {
		return nil, err
	}

	defs := make([]provider.ToolDefinition, 0, len(tools))
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
func (tb *ToolBroker) CallTool(ctx context.Context, agentID, toolName string, args map[string]any) (string, error) {
	// Check permissions.
	if !tb.CheckPermission(agentID, toolName) {
		return "", fmt.Errorf("tool %q denied for agent %q", toolName, agentID)
	}

	if tb.MCPManager == nil {
		return "", fmt.Errorf("no MCP manager configured")
	}

	return tb.MCPManager.ExecuteTool(ctx, toolName, args)
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

// ListTools returns all registered tools as provider.ToolDefinition.
func (tb *ToolBroker) ListTools() []provider.ToolDefinition {
	if tb.MCPManager == nil {
		return nil
	}
	return tb.MCPManager.GetAllTools()
}

// ListServers returns information about registered MCP servers.
func (tb *ToolBroker) ListServers() []mcp.ServerInfo {
	if tb.MCPManager == nil {
		return nil
	}
	return tb.MCPManager.ListServers()
}
