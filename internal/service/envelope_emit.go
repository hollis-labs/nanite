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

// Emit persists an EnvelopeInstance and pushes a "plugin_envelope" StreamEvent
// onto the session stream. The stream payload wraps the caller's payload
// inside {id, type, data} so EnvelopeRenderer routes to the correct component
// via envelope.type and the response POST references the row via envelope.id.
//
// Confirmed: EnvelopeRenderer.tsx (ui/src/components/chat/envelopes/) receives
// an Envelope with fields {id, type, data}. The wrapper shape here matches.
func (e *ApprovalEmitterImpl) Emit(ctx context.Context, sessionID, envelopeType string, payload []byte) (string, error) {
	inst := &store.EnvelopeInstance{
		SessionID:    sessionID,
		EnvelopeType: envelopeType,
		EnvelopeJSON: string(payload),
	}
	if err := e.store.CreateEnvelopeInstance(inst); err != nil {
		return "", fmt.Errorf("create envelope instance: %w", err)
	}

	streamWrap, err := json.Marshal(map[string]any{
		"id":   inst.ID,
		"type": envelopeType,
		"data": json.RawMessage(payload),
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
