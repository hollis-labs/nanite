package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/nanite/internal/dispatch"
)

// V1BuiltinPanelIDs is the locked v1 built-in panel catalog the chat agent
// can target with panel_open / panel_close. Per J8 v1 Decision
// Log (CW-20260426-0006) this is intentionally narrow: 3 drawers only.
//
//   - bottom_chat_drawer — sits below the chat transcript; long-form reference
//     content (documents, scratchpad). Wires to a separate FE store action,
//     not RightRailV2's tab strip.
//   - work — right-rail panel for todos / plans / sprint cards.
//   - workflows — right-rail panel for guided-interaction templates.
//
// Plugin-shipped panels (registered via plugin manifest panels[]) are also
// callable but require trusted H1 tier on the calling agent profile.
var V1BuiltinPanelIDs = map[string]bool{
	"bottom_chat_drawer": true,
	"work":               true,
	"workflows":          true,
}

// panelSignalEventType is the SSE stream event type the FE listens for to
// drive panel_open/panel_close/mode dispatch. The Envelope field on
// chat.StreamEvent carries the JSON payload (re-using the existing field to
// avoid widening the wire schema).
const panelSignalEventType = "panel_signal"

// PanelSignal is the JSON payload riding on a panel_signal stream event.
// Action vocabulary is intentionally small to keep the FE handler trivial.
type PanelSignal struct {
	// Action is "open" | "close" | "mode".
	Action string `json:"action"`
	// PanelID is the target panel ID for "open"/"close" actions. Empty for
	// "mode" signals (mode preset map drives the panel set).
	PanelID string `json:"panel_id,omitempty"`
	// Mode is the mode/status name for "mode" actions (e.g. "planning").
	// Empty for "open"/"close".
	Mode string `json:"mode,omitempty"`
	// Source identifies the originator. Always "agent" for tool-driven
	// signals; the FE uses this to attribute the open in its 4-state dismiss
	// machine (agent_opened vs user_opened).
	Source string `json:"source"`
}

// callPanelOpen handles panel_open. Validates the panel ID against the
// v1 built-in catalog and (for plugin-shipped panel IDs) gates on H1 trust.
// Returns a structured JSON result the agent can introspect: {opened, panel_id,
// reason?}. The FE applies the dismiss state machine — the backend NEVER
// silently drops the open; it always emits the panel_signal event so the FE
// owns the decision (the contract is "agent intent + FE policy").
func (st *SelfToolsTransport) callPanelOpen(ctx context.Context, args map[string]any) (*ToolResult, error) {
	panelID := strArg(args, "panel_id", "")
	if panelID == "" {
		return errorResult("panel_id is required"), nil
	}
	allowed, reason := st.resolvePanelAccess(ctx, panelID)
	if !allowed {
		return panelResultJSON(map[string]any{
			"opened":   false,
			"panel_id": panelID,
			"reason":   reason,
		}), nil
	}
	st.emitPanelSignal(ctx, args, PanelSignal{
		Action:  "open",
		PanelID: panelID,
		Source:  "agent",
	})
	return panelResultJSON(map[string]any{
		"opened":   true,
		"panel_id": panelID,
	}), nil
}

// callSignalMode handles signal_mode. The mode signal is broadcast as
// a panel_signal event with action="mode" and panel_id="" — the FE applies
// its preset map (panel-modes.ts) to translate the mode name into a set of
// panel opens. The backend never resolves the preset — the contract is
// "agent emits a hint, FE owns the policy". Unknown modes still emit; the
// FE no-ops on misses by design (graceful forward-compat).
func (st *SelfToolsTransport) callSignalMode(ctx context.Context, args map[string]any) (*ToolResult, error) {
	mode := strArg(args, "mode", "")
	if mode == "" {
		return errorResult("mode is required"), nil
	}
	st.emitPanelSignal(ctx, args, PanelSignal{
		Action: "mode",
		Mode:   mode,
		Source: "agent",
	})
	return panelResultJSON(map[string]any{
		"signaled": true,
		"mode":     mode,
	}), nil
}

// callPanelClose handles panel_close. Symmetric to callPanelOpen; the
// FE state machine refuses to close panels the user has manually opened.
func (st *SelfToolsTransport) callPanelClose(ctx context.Context, args map[string]any) (*ToolResult, error) {
	panelID := strArg(args, "panel_id", "")
	if panelID == "" {
		return errorResult("panel_id is required"), nil
	}
	allowed, reason := st.resolvePanelAccess(ctx, panelID)
	if !allowed {
		return panelResultJSON(map[string]any{
			"closed":   false,
			"panel_id": panelID,
			"reason":   reason,
		}), nil
	}
	st.emitPanelSignal(ctx, args, PanelSignal{
		Action:  "close",
		PanelID: panelID,
		Source:  "agent",
	})
	return panelResultJSON(map[string]any{
		"closed":   true,
		"panel_id": panelID,
	}), nil
}

// resolvePanelAccess returns (allowed, reason). Built-in v1 panel IDs are
// always allowed (visibility-only, low risk per J8 Decision Log §2). Plugin
// panel IDs are allowed only when:
//
//  1. The plugin host has a registered panel with that ID, AND
//  2. The caller's H1 trust resolves to TrustTrusted.
//
// Unknown panel IDs return (false, "unknown_panel"). Plugin-known IDs from
// untrusted callers return (false, "untrusted").
func (st *SelfToolsTransport) resolvePanelAccess(ctx context.Context, panelID string) (bool, string) {
	if V1BuiltinPanelIDs[panelID] {
		return true, ""
	}
	if st.PanelLookup == nil {
		// No plugin host wired (e.g. tests) — only built-ins are addressable.
		return false, "unknown_panel"
	}
	known := false
	for _, id := range st.PanelLookup() {
		if id == panelID {
			known = true
			break
		}
	}
	if !known {
		return false, "unknown_panel"
	}
	// Plugin-shipped panel — gate by H1 trust.
	if st.TrustResolver == nil {
		// No resolver wired — fall back to "untrusted" so plugin panels are
		// only addressable in fully-wired production builds. This matches the
		// safe default elsewhere in the codebase.
		return false, "untrusted"
	}
	wsID, apID := CallerProfileFromContext(ctx)
	if wsID == "" || apID == "" {
		return false, "untrusted"
	}
	tier, err := st.TrustResolver.ResolveTrust(ctx, wsID, apID)
	if err != nil {
		return false, "untrusted"
	}
	if tier != dispatch.TrustTrusted {
		return false, "untrusted"
	}
	return true, ""
}

// emitPanelSignal pushes a panel_signal stream event onto the originating
// session's chat stream. When no session ID is in the ctx (or the stream is
// not active) this is a quiet no-op — the agent's tool result still carries
// the {opened: true} confirmation so a transcript replay can reconstruct the
// intent from the tool call alone.
func (st *SelfToolsTransport) emitPanelSignal(ctx context.Context, args map[string]any, sig PanelSignal) {
	if st.PanelSignalSink == nil {
		return
	}
	sessionID := strArg(args, "session_id", "")
	if sessionID == "" {
		sessionID = SessionIDFromContext(ctx)
	}
	if sessionID == "" {
		return
	}
	payload, err := json.Marshal(sig)
	if err != nil {
		return
	}
	st.PanelSignalSink.BroadcastPanelSignal(sessionID, panelSignalEventType, string(payload))
}

// panelResultJSON builds a textResult containing the JSON-serialized payload.
// Centralizing this keeps the panel-handler returns uniform.
func panelResultJSON(payload map[string]any) *ToolResult {
	body, err := json.Marshal(payload)
	if err != nil {
		return errorResult(fmt.Sprintf("panel result marshal: %v", err))
	}
	return textResult(string(body))
}
