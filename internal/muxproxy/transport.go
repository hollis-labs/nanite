package muxproxy

import (
	"context"
	"encoding/json"
	"fmt"
)

// MuxService is the subset of service.MuxProxy the Transport depends on.
// Test seam.
type MuxService interface {
	ListAvailableLaunches(ctx context.Context) ([]LaunchSummary, error)
	LaunchSubordinate(ctx context.Context, launchID, nickname string) (LaunchResult, error)
	Send(ctx context.Context, sessionID, text string) (SendResult, error)
	Stop(ctx context.Context, sessionID string) error
}

// Transport dispatches the four mux_* tools to the MuxService.
type Transport struct {
	svc MuxService
}

// NewTransport constructs a Transport.
func NewTransport(svc MuxService) *Transport {
	return &Transport{svc: svc}
}

// ToolDef is the transport's local projection of the tool shape so
// the package stays decoupled from the mcp package.
type ToolDef struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// ListTools returns the four tool definitions.
func (t *Transport) ListTools(_ context.Context) ([]ToolDef, error) {
	defs := ToolDefinitions()
	out := make([]ToolDef, len(defs))
	for i, d := range defs {
		out[i] = ToolDef{Name: d.Name, Description: d.Description, InputSchema: d.InputSchema}
	}
	return out, nil
}

// CallTool dispatches by name. Returns raw JSON suitable for embedding
// in an MCP ToolResult text payload.
func (t *Transport) CallTool(ctx context.Context, name string, args map[string]any) (json.RawMessage, error) {
	switch name {
	case "mux_list_launches":
		res, err := t.svc.ListAvailableLaunches(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"launches": res})
	case "mux_launch":
		launchID, _ := args["launch_id"].(string)
		nickname, _ := args["nickname"].(string)
		res, err := t.svc.LaunchSubordinate(ctx, launchID, nickname)
		if err != nil {
			return nil, err
		}
		return json.Marshal(res)
	case "mux_send":
		sessionID, _ := args["session_id"].(string)
		text, _ := args["text"].(string)
		res, err := t.svc.Send(ctx, sessionID, text)
		if err != nil {
			return nil, err
		}
		return json.Marshal(res)
	case "mux_stop":
		sessionID, _ := args["session_id"].(string)
		if err := t.svc.Stop(ctx, sessionID); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"stopped": true})
	default:
		return nil, fmt.Errorf("muxproxy: unknown tool %q", name)
	}
}
