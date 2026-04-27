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
//
// H1 trust gate (CW-20260421-0014): the muxproxy.Transport.CallTool gate
// reads (workspace_id, agent_profile_id) from the context via
// muxproxy.WithCallerCtx. Those values are already carried on ctx by the
// service layer via mcp.WithCallerProfile (stamped in executeToolBatch
// alongside WithSessionID). We extract them here and re-stamp using the
// muxproxy convention so muxproxy.Transport stays independent of the mcp
// package while the gate fires correctly.
func (a *MuxTransportAdapter) CallTool(ctx context.Context, name string, args map[string]any) (*ToolResult, error) {
	// Propagate the caller profile from the mcp ctx convention into the
	// muxproxy ctx convention so the H1 trust gate in Transport.CallTool
	// can read it. No-op when ctx carries no profile (zero-value guard is
	// inside muxproxy.WithCallerCtx — it always stamps, but Transport.CallTool
	// only activates the gate when both fields are non-empty).
	wsID, apID := CallerProfileFromContext(ctx)
	if wsID != "" && apID != "" {
		ctx = muxproxy.WithCallerCtx(ctx, wsID, apID)
	}

	raw, err := a.Inner.CallTool(ctx, name, args)
	if err != nil {
		return nil, err
	}
	return &ToolResult{
		Content: []ToolContent{{Type: "text", Text: string(raw)}},
	}, nil
}
