package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/hollis-labs/conduit/internal/chat"
	"github.com/hollis-labs/conduit/internal/mcp"
	"github.com/hollis-labs/conduit/internal/provider"
	"github.com/hollis-labs/conduit/internal/toolclient"
)

// ToolSelection holds the result of tool selection, including progressive
// discovery metadata. Mirrors chat.toolSelection but is owned by the service layer.
type ToolSelection struct {
	Tools       []provider.ToolDefinition // tools to send to the LLM
	Catalog     string                    // non-empty when progressive discovery is active
	Progressive bool                      // true when using progressive discovery
}

// ToolResult holds the outcome of a single tool execution.
type ToolResult struct {
	Output  string // the raw result text
	IsError bool   // true if the tool call failed
}

// ToolService encapsulates tool selection, execution, and progressive discovery.
// It unifies the two duplicated execution branches (ToolClient path and
// MCPManager fallback path) from the old Engine into a single Execute method.
type ToolService interface {
	// SelectForAgent returns the tool set for an agent, applying intent
	// extraction, permission filtering, allowlist filtering, and progressive
	// discovery when the tool count exceeds the threshold.
	SelectForAgent(ctx context.Context, sessionID, agentID, userMessage, workspaceID string) (*ToolSelection, error)

	// Execute runs a tool call, routing through ToolClient (with permission
	// checks) when available, falling back to direct MCPManager execution.
	Execute(ctx context.Context, agentID, toolName string, input map[string]any) (*ToolResult, error)

	// HandleRequestTools processes a request_tools meta-tool call for
	// progressive discovery. Returns newly-discovered tool definitions and
	// a human-readable summary string.
	HandleRequestTools(ctx context.Context, input map[string]any) ([]provider.ToolDefinition, string, error)

	// ListSummaries returns lightweight name+description pairs for all
	// registered tools (no full schemas).
	ListSummaries() []toolclient.ToolSummary

	// GetToolMeta returns safety metadata for a tool. Returns false if the
	// tool is not found in the registry. Used by the permission engine.
	GetToolMeta(toolName string) (ToolMetaInfo, bool)
}

// ToolMetaInfo carries safety metadata for a tool, used by the permission
// engine and the parallel tool executor.
type ToolMetaInfo struct {
	IsReadOnly        bool
	IsDestructive     bool
	IsConcurrencySafe bool // safe to run in parallel with other tools
	MaxIterations     int  // 0 = no per-tool limit
}

// ProgressiveDiscoveryThreshold is the MCP tool count above which
// progressive discovery is activated. Matches the existing engine constant.
const ProgressiveDiscoveryThreshold = 5

// BrokerDecisionLogger logs tool selection decisions for debugging.
type BrokerDecisionLogger interface {
	LogBrokerDecision(sessionID, intent, layerReached string, selectedTools []string, signals string) error
}

// toolServiceImpl is the concrete implementation of ToolService.
type toolServiceImpl struct {
	toolClient     *toolclient.ToolClient
	mcpManager     *mcp.Manager
	agents         AgentReader
	decisionLogger BrokerDecisionLogger
}

// NewToolService creates a ToolService. Both toolClient and mcpManager may be
// nil — Execute will return an error if neither is available.
func NewToolService(tc *toolclient.ToolClient, mcpMgr *mcp.Manager, agents AgentReader) ToolService {
	return &toolServiceImpl{
		toolClient: tc,
		mcpManager: mcpMgr,
		agents:     agents,
	}
}

// SetDecisionLogger attaches a broker decision logger (typically *store.Store).
func (s *toolServiceImpl) SetDecisionLogger(dl BrokerDecisionLogger) {
	s.decisionLogger = dl
}

// SelectForAgent implements ToolService.
func (s *toolServiceImpl) SelectForAgent(ctx context.Context, sessionID, agentID, userMessage, workspaceID string) (*ToolSelection, error) {
	intent, hints := extractIntent(userMessage)
	log.Printf("service/tool: extracted intent=%q hints=%v", intent, hints)

	// Collect tools via broker selection.
	var allTools []provider.ToolDefinition
	seen := map[string]bool{} // dedup: Anthropic API rejects duplicate tool names

	if s.toolClient != nil {
		selected, err := s.toolClient.SelectToolsAsProvider(ctx, intent, hints, workspaceID, agentID)
		if err != nil {
			log.Printf("service/tool: broker selection failed: %v — falling back to MCP manager", err)
		} else {
			for _, t := range selected {
				if !seen[t.Name] {
					seen[t.Name] = true
					allTools = append(allTools, t)
				}
			}
		}
	}

	// If no MCP tools from the broker, try direct discovery from agent's configured servers.
	mcpCount := countMCPTools(allTools)
	if mcpCount == 0 && s.mcpManager != nil && s.agents != nil {
		agent, err := s.agents.GetAgent(agentID)
		if err == nil {
			allTools, seen = s.discoverAgentMCPTools(ctx, agent.MCPServers, allTools, seen)
		}
	}

	// Apply agent tools allowlist (schema v2).
	if s.agents != nil {
		if agent, err := s.agents.GetAgent(agentID); err == nil {
			allTools = filterToolsByAllowlist(allTools, agent.Tools)
		}
	}

	if len(allTools) == 0 {
		log.Printf("service/tool: WARNING 0 tools for agent %s — proceeding without tools", agentID)
	} else {
		log.Printf("service/tool: selected %d tools for agent %s", len(allTools), agentID)
	}

	// Check if progressive discovery should be used.
	mcpToolCount := countMCPTools(allTools)
	if mcpToolCount > ProgressiveDiscoveryThreshold && s.toolClient != nil {
		summaries := s.toolClient.ListToolSummaries()
		catalog := chat.BuildToolCatalog(summaries)

		// Keep builtin (non-MCP) tools alongside request_tools meta-tool.
		builtinTools := []provider.ToolDefinition{toolclient.RequestToolsMetaTool()}
		for _, t := range allTools {
			if !strings.HasPrefix(t.Name, "mcp__") {
				builtinTools = append(builtinTools, t)
			}
		}

		log.Printf("service/tool: progressive discovery active — %d MCP tools, %d builtins kept, %d catalog entries",
			mcpToolCount, len(builtinTools)-1, len(summaries))

		s.logDecision(sessionID, intent, "progressive", builtinTools)
		return &ToolSelection{
			Tools:       builtinTools,
			Catalog:     catalog,
			Progressive: true,
		}, nil
	}

	layer := "broker"
	if len(allTools) == 0 {
		layer = "empty"
	}
	s.logDecision(sessionID, intent, layer, allTools)
	return &ToolSelection{Tools: allTools}, nil
}

// logDecision persists the broker selection decision for the debug panel.
func (s *toolServiceImpl) logDecision(sessionID, intent, layer string, tools []provider.ToolDefinition) {
	if s.decisionLogger == nil || sessionID == "" {
		return
	}
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	if err := s.decisionLogger.LogBrokerDecision(sessionID, intent, layer, names, ""); err != nil {
		log.Printf("service/tool: failed to log broker decision: %v", err)
	}
}

// Execute implements ToolService. Unified execution path: ToolClient (with
// permissions) → MCPManager fallback → error.
func (s *toolServiceImpl) Execute(ctx context.Context, agentID, toolName string, input map[string]any) (*ToolResult, error) {
	if s.toolClient != nil {
		result, err := s.toolClient.CallTool(ctx, agentID, toolName, input)
		if err != nil {
			return &ToolResult{Output: fmt.Sprintf("Error: %v", err), IsError: true}, nil
		}
		return &ToolResult{Output: result}, nil
	}

	if s.mcpManager != nil {
		result, err := s.mcpManager.ExecuteTool(ctx, toolName, input)
		if err != nil {
			return &ToolResult{Output: fmt.Sprintf("Error: %v", err), IsError: true}, nil
		}
		return &ToolResult{Output: result}, nil
	}

	return &ToolResult{
		Output:  "Error: no tool client or MCP manager configured",
		IsError: true,
	}, nil
}

// HandleRequestTools implements ToolService.
func (s *toolServiceImpl) HandleRequestTools(_ context.Context, input map[string]any) ([]provider.ToolDefinition, string, error) {
	if s.toolClient == nil {
		return nil, "No tool client configured.", fmt.Errorf("no tool client configured")
	}
	tools, summary := s.toolClient.HandleRequestTools(input)
	return tools, summary, nil
}

// ListSummaries implements ToolService.
func (s *toolServiceImpl) ListSummaries() []toolclient.ToolSummary {
	if s.toolClient == nil {
		return nil
	}
	return s.toolClient.ListToolSummaries()
}

// GetToolMeta implements ToolService. Returns safety metadata for a tool.
// Uses name-based heuristics consistent with internal/tool/adapt.go patterns.
// Concurrency safety: read/search/fetch tools are safe; write/edit/bash are not.
func (s *toolServiceImpl) GetToolMeta(toolName string) (ToolMetaInfo, bool) {
	meta := ToolMetaInfo{}

	// Read-only tools.
	switch {
	case strings.HasSuffix(toolName, "_read") || strings.HasSuffix(toolName, "_glob") ||
		strings.HasSuffix(toolName, "_grep") || strings.HasSuffix(toolName, "_search") ||
		strings.HasSuffix(toolName, "_list") || strings.HasSuffix(toolName, "_get"):
		meta.IsReadOnly = true
	case strings.Contains(toolName, "web_fetch") || strings.Contains(toolName, "web_search"):
		meta.IsReadOnly = true
	}

	// Destructive tools.
	switch {
	case strings.Contains(toolName, "delete") || strings.Contains(toolName, "remove"):
		meta.IsDestructive = true
	case strings.Contains(toolName, "shell") || strings.Contains(toolName, "bash"):
		meta.IsDestructive = true // shell commands may be destructive
	case strings.Contains(toolName, "drop") || strings.Contains(toolName, "reset"):
		meta.IsDestructive = true
	}

	// Concurrency safety — mirrors internal/tool/adapt.go:inferSafetyOptions.
	// Read-only tools are concurrent-safe; write/edit/bash/destructive are not.
	switch {
	case meta.IsReadOnly:
		meta.IsConcurrencySafe = true
	case strings.Contains(toolName, "json_parse") || strings.Contains(toolName, "datetime") ||
		strings.Contains(toolName, "hash") || strings.Contains(toolName, "uuid"):
		meta.IsConcurrencySafe = true // pure utility tools
	default:
		meta.IsConcurrencySafe = false
	}

	return meta, true
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// discoverAgentMCPTools performs direct MCP discovery from an agent's
// configured server list. Returns the updated tool slice and seen map.
func (s *toolServiceImpl) discoverAgentMCPTools(
	ctx context.Context,
	mcpServersJSON string,
	allTools []provider.ToolDefinition,
	seen map[string]bool,
) ([]provider.ToolDefinition, map[string]bool) {
	var servers []string
	// Silently ignore bad JSON — matches existing engine behaviour.
	_ = parseJSONStrings(mcpServersJSON, &servers)

	beforeCount := countMCPTools(allTools)
	for _, srv := range servers {
		srvTools, err := s.mcpManager.DiscoverServerTools(ctx, srv)
		if err != nil {
			continue
		}
		for _, t := range srvTools {
			name := fmt.Sprintf("mcp__%s__%s", srv, t.Name)
			if seen[name] {
				continue
			}
			seen[name] = true
			allTools = append(allTools, provider.ToolDefinition{
				Name:        name,
				Description: t.Description,
				InputSchema: t.InputSchema,
			})
		}
	}
	afterCount := countMCPTools(allTools)
	if afterCount > beforeCount {
		log.Printf("service/tool: direct MCP discovery added %d tools from configured servers", afterCount-beforeCount)
	}
	return allTools, seen
}

// countMCPTools counts tools with the "mcp__" prefix.
func countMCPTools(tools []provider.ToolDefinition) int {
	n := 0
	for _, t := range tools {
		if strings.HasPrefix(t.Name, "mcp__") {
			n++
		}
	}
	return n
}

// filterToolsByAllowlist removes tools not in the agent's tools allowlist.
// An empty or "[]" allowlist means no filtering.
func filterToolsByAllowlist(tools []provider.ToolDefinition, allowlistJSON string) []provider.ToolDefinition {
	if allowlistJSON == "" || allowlistJSON == "[]" {
		return tools
	}
	var allowlist []string
	if err := parseJSONStrings(allowlistJSON, &allowlist); err != nil || len(allowlist) == 0 {
		return tools
	}
	filtered := make([]provider.ToolDefinition, 0, len(tools))
	for _, t := range tools {
		for _, pattern := range allowlist {
			if toolclient.MatchPattern(pattern, t.Name) {
				filtered = append(filtered, t)
				break
			}
		}
	}
	log.Printf("service/tool: allowlist filtered %d → %d tools", len(tools), len(filtered))
	return filtered
}

// extractIntent derives an intent string and keyword hints from a user message.
// Mirrors chat.ExtractIntent logic — placed here so the service layer doesn't
// depend on the chat package.
func extractIntent(userMessage string) (intent string, hints []string) {
	msg := strings.ToLower(userMessage)
	for _, ch := range []string{",", ".", "!", "?", ";", ":", "'", "\"", "(", ")", "[", "]", "{", "}", "\n", "\t"} {
		msg = strings.ReplaceAll(msg, ch, " ")
	}

	words := strings.Fields(msg)
	seen := make(map[string]bool)
	var keywords []string

	for _, w := range words {
		if len(w) < 3 {
			continue
		}
		if intentStopWords[w] {
			continue
		}
		if seen[w] {
			continue
		}
		seen[w] = true
		keywords = append(keywords, w)
		if len(keywords) >= 10 {
			break
		}
	}

	if len(keywords) == 0 {
		return "general", nil
	}

	intentWords := keywords
	if len(intentWords) > 3 {
		intentWords = intentWords[:3]
	}
	return strings.Join(intentWords, " "), keywords
}

// intentStopWords mirrors the set from chat.ExtractIntent.
var intentStopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true,
	"that": true, "this": true, "from": true, "have": true,
	"has": true, "not": true, "but": true, "are": true,
	"was": true, "were": true, "been": true, "can": true,
	"could": true, "would": true, "should": true, "will": true,
	"does": true, "did": true, "its": true, "they": true,
	"them": true, "their": true, "there": true, "what": true,
	"when": true, "where": true, "which": true, "who": true,
	"how": true, "all": true, "each": true, "every": true,
	"any": true, "some": true, "more": true, "most": true,
	"also": true, "than": true, "then": true, "just": true,
	"about": true, "into": true, "over": true, "such": true,
	"please": true, "want": true, "need": true, "like": true,
	"know": true, "think": true, "make": true, "use": true,
	"using": true, "help": true, "show": true, "tell": true,
}

// parseJSONStrings is a tiny helper to unmarshal a JSON string array.
func parseJSONStrings(raw string, out *[]string) error {
	if raw == "" {
		return nil
	}
	return json.Unmarshal([]byte(raw), out)
}
