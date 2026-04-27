package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/nanite/internal/elicitation"
	"github.com/hollis-labs/nanite/internal/store"
)

// ElicitationEmitterImpl implements elicitation.Emitter using the approval
// emit path (persist envelope instance + push SSE stream event). This ensures
// the elicitation-prompt card appears inline in chat AND the response endpoint
// can look up the envelope row by ID.
//
// CW-20260420-0018 — G4 elicitation/create.
type ElicitationEmitterImpl struct {
	approval *ApprovalEmitterImpl
}

// NewElicitationEmitter constructs an ElicitationEmitterImpl backed by an
// ApprovalEmitterImpl (which owns the store + stream manager wiring).
func NewElicitationEmitter(s *store.Store, sm *StreamManager) *ElicitationEmitterImpl {
	return &ElicitationEmitterImpl{
		approval: NewApprovalEmitter(s, sm),
	}
}

// EmitElicitationPrompt persists an elicitation-prompt envelope instance and
// pushes it onto the session's live stream so the UI renders the card
// immediately. Returns the envelope instance ID for response routing.
func (e *ElicitationEmitterImpl) EmitElicitationPrompt(ctx context.Context, input elicitation.EmitInput) (string, error) {
	b, err := json.Marshal(input.Data)
	if err != nil {
		return "", fmt.Errorf("elicitation emitter: marshal payload: %w", err)
	}
	id, err := e.approval.Emit(ctx, input.SessionID, "elicitation-prompt", b)
	if err != nil {
		return "", fmt.Errorf("elicitation emitter: emit: %w", err)
	}
	return id, nil
}
