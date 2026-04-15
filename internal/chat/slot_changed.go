package chat

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/nanite/internal/store"
)

// SlotChangedKind is the envelope kind for slot-pipeline transitions.
const SlotChangedKind = "slot_changed"

// SlotChangeKind enumerates the kinds of slot transitions the pipeline can
// emit. Each value corresponds to a frontend modal explanation.
const (
	SlotChangeSummarized  = "summarized"
	SlotChangeDropped     = "dropped"
	SlotChangeHydrated    = "hydrated"
	SlotChangeTruncated   = "truncated"
	SlotChangeRepopulated = "repopulated"
)

// SlotChangedV1 is the payload schema for slot_changed envelopes. It captures
// what the slot pipeline did during a turn so the frontend can render an
// inline notification card with a "What is this?" link to the hot-swap
// explainer modal.
type SlotChangedV1 struct {
	V            int    `json:"v"`
	Slot         string `json:"slot"`
	Change       string `json:"change"`
	Reasoning    string `json:"reasoning"`
	TokensBefore int    `json:"tokens_before"`
	TokensAfter  int    `json:"tokens_after"`
	HelpLink     string `json:"help_link,omitempty"`
}

// SlotChangedV1Version is the schema version emitted on the wire.
const SlotChangedV1Version = 1

// SlotChangedDefaultHelpLink points to the hot-swap explainer modal.
const SlotChangedDefaultHelpLink = "/help/hot-swap"

// EmitSlotChangedEvent constructs and sends a slot_changed StreamEvent to the
// chat stream. Callers fill the payload; this helper marshals the envelope
// JSON and forwards it via the existing envelope-style channel.
func EmitSlotChangedEvent(stream chan<- StreamEvent, payload SlotChangedV1) error {
	if stream == nil {
		return nil
	}
	if payload.V == 0 {
		payload.V = SlotChangedV1Version
	}
	if payload.HelpLink == "" {
		payload.HelpLink = SlotChangedDefaultHelpLink
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal slot_changed: %w", err)
	}
	stream <- StreamEvent{
		Type:     SlotChangedKind,
		Envelope: string(body),
	}
	return nil
}

// slotChangedResponseHandler is registered in init so the response endpoint
// has a handler if the frontend ever POSTs back (e.g., a user-acknowledged
// dismissal). Today the card is informational only — the default behaviour
// is identical to defaultResponseHandler.
type slotChangedResponseHandler struct{}

func (slotChangedResponseHandler) HandleResponse(_ context.Context, _ store.EnvelopeInstance, resp ResponseV1) (HandlerResult, error) {
	data := make(map[string]any, len(resp.Data))
	for k, v := range resp.Data {
		data[k] = v
	}
	return HandlerResult{TranscriptData: data, Silent: true}, nil
}

func init() {
	RegisterResponseHandler(SlotChangedKind, slotChangedResponseHandler{})
}
