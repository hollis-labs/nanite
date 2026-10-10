package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	condmcp "github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/mcpbridge"
	"github.com/hollis-labs/nanite/internal/selftools"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

func unsupportedProxyExecutor(tool condmcp.Tool) bool {
	return tool.Name == "python_run" || tool.Name == "workflow_execute_tool_step"
}

func coreProxyCatalog(tools []condmcp.Tool) []condmcp.Tool {
	tools = slices.DeleteFunc(tools, unsupportedProxyExecutor)
	for i := range tools {
		switch tools[i].Name {
		case "tool_list":
			tools[i].Description = "List retained core tools available through this MCP proxy. Plugin discovery requires verified bridge authority."
		case "tool_describe":
			tools[i].Description = "Describe a retained core tool available through this MCP proxy."
		case "tool_validate":
			tools[i].Description = "Validate arguments against a retained core tool's schema."
		}
	}
	return tools
}

type proxyCoreDeclarations []condmcp.Tool

func (d proxyCoreDeclarations) LookupToolInputSchema(name string) (map[string]any, bool) {
	for _, tool := range d {
		if tool.Name == name {
			return tool.InputSchema, true
		}
	}
	return nil, false
}
func (d proxyCoreDeclarations) GetAllToolsUnfiltered() []llmtypes.ToolDefinition {
	out := make([]llmtypes.ToolDefinition, 0, len(d))
	for _, tool := range d {
		out = append(out, llmtypes.ToolDefinition{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema})
	}
	return out
}

// coreMetadata has no host connection, manager, datastore, permission issuer or
// dispatch collaborator. Its only registry is the retained static core surface.
func (p *selfToolProxy) coreMetadata(ctx context.Context, name string, args map[string]any) (*condmcp.ToolResult, error) {
	targetKey := "name"
	if name == "tool_validate" {
		targetKey = "tool_name"
	}
	target, _ := args[targetKey].(string)
	if name != "tool_list" && target != "" && !p.advertises(target) {
		return nil, fmt.Errorf("authority_unavailable: %w", mcpbridge.ErrAuthorityUnavailable)
	}
	// Snapshot static declarations; handlers may normalize schema maps. Their
	// local self-definition fallback is filtered before returning a tool list.
	raw, err := json.Marshal(p.catalog)
	if err != nil {
		return nil, err
	}
	var declarations proxyCoreDeclarations
	if decodeErr := json.Unmarshal(raw, &declarations); decodeErr != nil {
		return nil, decodeErr
	}
	local := &selftools.SelfToolsTransport{Inventory: declarations, SchemaLookup: declarations}
	if name != "tool_list" {
		return local.CallTool(ctx, name, args)
	}
	listArgs := make(map[string]any, len(args)+1)
	for key, value := range args {
		listArgs[key] = value
	}
	// Gather the bounded static definitions before applying the launch scope and
	// caller's display limit. No live/global discovery source can enter this list.
	listArgs["limit"] = 1 << 20
	result, err := local.CallTool(ctx, name, listArgs)
	if err != nil || result == nil || result.IsError {
		return result, err
	}
	var listed struct {
		Tools []struct {
			Name    string `json:"name"`
			Summary string `json:"summary"`
		} `json:"tools"`
	}
	if len(result.Content) != 1 || json.Unmarshal([]byte(result.Content[0].Text), &listed) != nil {
		return nil, mcpbridge.ErrTargetUnavailable
	}
	filtered := listed.Tools[:0]
	for _, tool := range listed.Tools {
		if p.advertises(tool.Name) {
			filtered = append(filtered, tool)
		}
	}
	if filtered == nil {
		filtered = make([]struct {
			Name    string `json:"name"`
			Summary string `json:"summary"`
		}, 0)
	}
	out := map[string]any{"tools": filtered, "count": len(filtered)}
	limit := condmcp.IntArg(args, "limit", 100)
	if limit <= 0 {
		limit = 100
	}
	filter, _ := args["filter"].(string)
	if strings.TrimSpace(filter) == "" && len(filtered) > limit {
		out["tools"], out["count"], out["total"], out["truncated"] = filtered[:limit], limit, len(filtered), true
		out["hint"] = "Narrow with filter or raise limit to see more core tools."
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return condmcp.TextResult(string(body)), nil
}
