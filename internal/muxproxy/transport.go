//go:build devmode

package muxproxy

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/nanite/internal/dispatch"
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
	svc           MuxService
	trustResolver dispatch.TrustResolver
}

// SetTrustResolver wires the H1 trust resolver. Call before any tool dispatch.
// When nil, all mux_* calls are permitted (no runtime trust gate).
func (t *Transport) SetTrustResolver(r dispatch.TrustResolver) { t.trustResolver = r }

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

// callerCtxKey is the context key for the (workspace_id, agent_profile_id)
// pair attached by the MCP layer before tool dispatch. Used by the H1
// trust gate to resolve the caller's tier without changing CallTool's
// signature.
type callerCtxKey struct{}

// callerCtx holds the workspace and agent profile IDs of the session that
// invoked a mux_* tool.
type callerCtx struct {
	WorkspaceID    string
	AgentProfileID string
}

// WithCallerCtx returns a new context carrying the caller workspace/agent IDs.
// The MCP adapter calls this before invoking CallTool.
func WithCallerCtx(ctx context.Context, workspaceID, agentProfileID string) context.Context {
	return context.WithValue(ctx, callerCtxKey{}, callerCtx{workspaceID, agentProfileID})
}

// callerCtxFromContext extracts the caller context. Both fields are empty
// when none was stamped.
func callerCtxFromContext(ctx context.Context) callerCtx {
	v, _ := ctx.Value(callerCtxKey{}).(callerCtx)
	return v
}

// CallTool dispatches by name. Returns raw JSON suitable for embedding
// in an MCP ToolResult text payload.
//
// H1 trust gate: when a TrustResolver is wired, CallerCtx must carry a
// WorkspaceID + AgentProfileID. Untrusted callers receive an error.
// Normal callers proceed (approval for mux tools is handled at the
// subagent layer, not here). Trusted callers proceed silently.
func (t *Transport) CallTool(ctx context.Context, name string, args map[string]any) (json.RawMessage, error) {
	// H1: enforce runtime trust gate for mux_* tools.
	if t.trustResolver != nil {
		caller := callerCtxFromContext(ctx)
		if caller.WorkspaceID != "" && caller.AgentProfileID != "" {
			tier, err := t.trustResolver.ResolveTrust(ctx, caller.WorkspaceID, caller.AgentProfileID)
			if err != nil {
				// Fail closed: treat resolve error as untrusted to prevent
				// silent escalation.
				return nil, fmt.Errorf("muxproxy: trust resolve error for tool %q: %w", name, err)
			}
			if tier == dispatch.TrustUntrusted {
				return nil, fmt.Errorf("muxproxy: %w — tool %q requires at least normal trust",
					dispatch.ErrUntrustedRole, name)
			}
		}
	}

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
