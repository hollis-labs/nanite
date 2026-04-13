package mcp

import (
	"context"
	"fmt"

	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
	sdksub "github.com/hollis-labs/plugin-sdk/subprocess"
)

// PluginMCPTransport implements MCPTransport by proxying tools/list and
// tools/call RPCs to a subprocess plugin over its existing JSON-RPC
// Transport. This lets a subprocess plugin expose an MCP server to the host
// without running a separate stdio/http transport — the plugin answers the
// mcp/list_tools and mcp/call_tool methods defined in plugin-sdk.
//
// Params carry the logical server name so a single plugin may register
// multiple MCP server namespaces over one transport; the plugin is expected
// to dispatch based on that field.
type PluginMCPTransport struct {
	transport  *subprocess.Transport
	serverName string
}

// pluginListToolsParams is the JSON body sent on mcp/list_tools.
type pluginListToolsParams struct {
	Server string `json:"server"`
}

// pluginListToolsResult is the expected reply shape — a Tools slice aligned
// with the MCP tools/list response so the existing Tool struct deserializes.
type pluginListToolsResult struct {
	Tools []Tool `json:"tools"`
}

// pluginCallToolParams is the JSON body sent on mcp/call_tool.
type pluginCallToolParams struct {
	Server    string         `json:"server"`
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// NewPluginMCPTransport builds a transport that proxies MCP calls to the
// given subprocess plugin transport under the named MCP server namespace.
func NewPluginMCPTransport(t *subprocess.Transport, serverName string) *PluginMCPTransport {
	return &PluginMCPTransport{
		transport:  t,
		serverName: serverName,
	}
}

// ListTools proxies mcp/list_tools to the subprocess plugin.
func (p *PluginMCPTransport) ListTools(ctx context.Context) ([]Tool, error) {
	if p.transport == nil {
		return nil, fmt.Errorf("plugin mcp transport %q: nil subprocess transport", p.serverName)
	}
	result, err := subprocess.CallResult[pluginListToolsResult](
		p.transport, ctx, sdksub.MethodListTools, &pluginListToolsParams{Server: p.serverName},
	)
	if err != nil {
		return nil, fmt.Errorf("mcp/list_tools on plugin server %q: %w", p.serverName, err)
	}
	return result.Tools, nil
}

// CallTool proxies mcp/call_tool to the subprocess plugin.
//
// B.10: the method constant was renamed MethodCallTool → MethodMCPCallTool in
// plugin-sdk v0.2.0 and the canonical wire types are now sdksub.MCPCallRequest
// / sdksub.MCPCallResult. We keep the pluginCallToolParams shape here because
// Nanite's plugin MCP servers are namespaced (multiple logical servers per
// plugin transport) — the plugin dispatches on the Server field. The SDK's
// MCPCallRequest is single-server; plugin-side routing is an upstream concern
// if/when SDK adopts namespacing. For now we keep the local param shape and
// decode into a compatible result (ToolResult covers the IsError/Content pair;
// envelope propagation is a TODO tracked for B.12).
func (p *PluginMCPTransport) CallTool(ctx context.Context, name string, arguments map[string]any) (*ToolResult, error) {
	if p.transport == nil {
		return nil, fmt.Errorf("plugin mcp transport %q: nil subprocess transport", p.serverName)
	}
	result, err := subprocess.CallResult[ToolResult](
		p.transport, ctx, sdksub.MethodMCPCallTool, &pluginCallToolParams{
			Server:    p.serverName,
			Tool:      name,
			Arguments: arguments,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("mcp/call_tool %s on plugin server %q: %w", name, p.serverName, err)
	}
	// TODO(B.12): if the plugin returns structured envelopes on MCPCallResult,
	// plumb them into the chat stream. Today ToolResult is decoded directly
	// from the plugin's reply, which is IsError/Content-shaped; any envelopes
	// field the plugin populates is dropped on the host side.
	return result, nil
}
