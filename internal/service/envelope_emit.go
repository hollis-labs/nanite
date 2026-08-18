package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

// ApprovalEmitterImpl is the concrete emitter G-4 uses to persist an envelope
// instance AND push it onto the parent session's live stream so the FE renders
// the card AND the /api/envelopes/:id/respond endpoint can resolve the row.
//
// Discovered gap (2026-04-17): CreateEnvelopeInstance has no production writers
// today; the plugin-envelope path streams but does not persist. G-4 is the first
// caller of the persist+stream pair.
type ApprovalEmitterImpl struct {
	store   *store.Store
	streams *StreamManager
}

// NewApprovalEmitter returns a configured ApprovalEmitterImpl.
func NewApprovalEmitter(s *store.Store, sm *StreamManager) *ApprovalEmitterImpl {
	return &ApprovalEmitterImpl{store: s, streams: sm}
}

// EnvelopeRouting carries the optional panel-routing hints surfaced on the
// plugin_envelope stream wrapper. These fields are read by the FE's
// applyEnvelopePanelEffects (ui/src/lib/panel-signal.ts) to drive
// drawer/panel side effects. Empty strings are omitted from the wire shape.
type EnvelopeRouting struct {
	Target              string
	RenderTarget        string
	RenderTargetBlocked string
	Mode                string
	DisplayClass        EnvelopeDisplayClass
	// DevModeOnly marks an envelope that is only emitted when developer mode
	// is enabled (the original example, chat-loop-budget-soft-warning, was
	// removed by Phase 0 item 12, 2026-08-18). The FE renders a small "DEV"
	// badge so operators recognize it as dev-mode telemetry rather than a
	// real alert. Wire field `dev_mode_only`; omitted when false
	// (CW-20260517-0008).
	DevModeOnly bool
}

// Emit persists an EnvelopeInstance and pushes a "plugin_envelope" StreamEvent
// onto the session stream. The stream payload wraps the caller's payload
// inside {id, type, data} so EnvelopeRenderer routes to the correct component
// via envelope.type and the response POST references the row via envelope.id.
//
// Confirmed: EnvelopeRenderer.tsx (ui/src/components/chat/envelopes/) receives
// an Envelope with fields {id, type, data}. Approval/elicitation envelopes
// don't carry panel routing today, so EnvelopeRouting is zero-valued and the
// optional routing fields are omitted from the wrapper.
func (e *ApprovalEmitterImpl) Emit(ctx context.Context, sessionID, envelopeType string, payload []byte) (string, error) {
	inst := &store.EnvelopeInstance{
		SessionID:    sessionID,
		EnvelopeType: envelopeType,
		EnvelopeJSON: string(payload),
	}
	if err := e.store.CreateEnvelopeInstance(inst); err != nil {
		return "", fmt.Errorf("create envelope instance: %w", err)
	}

	streamWrap, err := buildPluginEnvelopeWrap(inst.ID, envelopeType, payload, EnvelopeRouting{
		DisplayClass: EnvelopeDisplayClassActionRequired,
	})
	if err != nil {
		return "", fmt.Errorf("marshal stream wrap: %w", err)
	}

	e.streams.BroadcastSessionStreamEvent(sessionID, chat.StreamEvent{
		Type:     "plugin_envelope",
		Envelope: string(streamWrap),
	})
	slog.Info("envelope emitted", "session_id", sessionID, "type", envelopeType, "id", inst.ID)
	return inst.ID, nil
}

// buildPluginEnvelopeWrap produces the {id, type, data, target, render_target,
// mode, render_target_blocked, display_class, dev_mode_only} wire shape
// consumed by useChat's plugin_envelope handler. Routing fields come from the caller (not from inside
// `payload`, which is the envelope's `data` blob — routing lives at
// envelope-level, not inside `data`). Empty optional fields are omitted so
// consumers can rely on presence to signal intent.
//
// Used by ApprovalEmitterImpl.Emit (always zero-valued routing) and by
// chat_generate.go's broadcastShowCardEnvelopeEvents (passes parsed routing
// hints from chat.Envelope). Keeping a single helper avoids drift between the
// two emit sites — the lossy {id, type, data}-only projection was the parallel
// of the c110/CW-20260429-0019 EnvelopeRef bug, just on the live wire.
func buildPluginEnvelopeWrap(id, envelopeType string, payload []byte, routing EnvelopeRouting) ([]byte, error) {
	wrap := map[string]any{
		"id":   id,
		"type": envelopeType,
		"data": json.RawMessage(payload),
	}
	if routing.Target != "" {
		wrap["target"] = routing.Target
	}
	if routing.RenderTarget != "" {
		wrap["render_target"] = routing.RenderTarget
	}
	if routing.RenderTargetBlocked != "" {
		wrap["render_target_blocked"] = routing.RenderTargetBlocked
	}
	if routing.Mode != "" {
		wrap["mode"] = routing.Mode
	}
	if routing.DisplayClass != "" {
		wrap["display_class"] = string(routing.DisplayClass)
	}
	if routing.DevModeOnly {
		wrap["dev_mode_only"] = true
	}
	return json.Marshal(wrap)
}
