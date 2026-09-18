package mcp

import (
	"encoding/json"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// convertSDKTool maps the official SDK's tool definition onto nanite's.
//
// Annotations round-trip through JSON rather than being copied field by
// field: Manager.ToolBehavior reads them as a map keyed by the wire names, and
// a hand-written copy would silently drop any hint the SDK learns about later.
func convertSDKTool(tool *sdkmcp.Tool) (Tool, error) {
	out := Tool{
		Name:        tool.Name,
		Description: tool.Description,
	}

	if tool.InputSchema != nil {
		schema, err := toStringMap(tool.InputSchema)
		if err != nil {
			return Tool{}, fmt.Errorf("tool %q input schema: %w", tool.Name, err)
		}
		out.InputSchema = schema
	}

	if tool.Annotations != nil {
		annotations, err := toStringMap(tool.Annotations)
		if err != nil {
			return Tool{}, fmt.Errorf("tool %q annotations: %w", tool.Name, err)
		}
		out.Annotations = annotations
	}

	return out, nil
}

func toStringMap(v any) (map[string]any, error) {
	if m, ok := v.(map[string]any); ok {
		return m, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// convertSDKCallResult maps the official SDK's tool result onto nanite's.
//
// nanite's ToolContent carries text only. Non-text blocks (images, embedded
// resources) are kept as a placeholder of their own type rather than dropped,
// so a result that consisted only of them does not reach the agent as an empty
// success.
func convertSDKCallResult(res *sdkmcp.CallToolResult) *ToolResult {
	out := &ToolResult{IsError: res.IsError}
	for _, c := range res.Content {
		switch content := c.(type) {
		case *sdkmcp.TextContent:
			out.Content = append(out.Content, ToolContent{Type: "text", Text: content.Text})
		case nil:
			continue
		default:
			raw, err := c.MarshalJSON()
			if err != nil {
				out.Content = append(out.Content, ToolContent{
					Type: "text",
					Text: "[unrepresentable content block]",
				})
				continue
			}
			out.Content = append(out.Content, ToolContent{Type: "text", Text: string(raw)})
		}
	}
	// A structured-only result carries its payload nowhere else, and the
	// agent surface is text.
	if len(out.Content) == 0 && res.StructuredContent != nil {
		if raw, err := json.Marshal(res.StructuredContent); err == nil {
			out.Content = append(out.Content, ToolContent{Type: "text", Text: string(raw)})
		}
	}
	return out
}
