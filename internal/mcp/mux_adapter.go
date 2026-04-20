package mcp

import (
	"context"

	"github.com/hollis-labs/nanite/internal/muxproxy"
)

// MuxTransportAdapter wraps a *muxproxy.Transport for registration as
// an mcp Transport. POC for CW-20260420-0047.
type MuxTransportAdapter struct {
	Inner *muxproxy.Transport
}

// ListTools converts muxproxy.ToolDef to mcp.Tool.
func (a *MuxTransportAdapter) ListTools(ctx context.Context) ([]Tool, error) {
	defs, err := a.Inner.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Tool, len(defs))
	for i, d := range defs {
		out[i] = Tool{
			Name:        d.Name,
			Description: d.Description,
			InputSchema: d.InputSchema,
		}
	}
	return out, nil
}

// CallTool dispatches to muxproxy's transport and wraps the returned
// JSON in an mcp.ToolResult text content block.
func (a *MuxTransportAdapter) CallTool(ctx context.Context, name string, args map[string]any) (*ToolResult, error) {
	raw, err := a.Inner.CallTool(ctx, name, args)
	if err != nil {
		return nil, err
	}
	return &ToolResult{
		Content: []ToolContent{{Type: "text", Text: string(raw)}},
	}, nil
}
