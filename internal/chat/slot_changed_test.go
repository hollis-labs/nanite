package chat

import (
	"encoding/json"
	"testing"
)

func TestEmitSlotChangedEvent_DefaultsAndPayload(t *testing.T) {
	ch := make(chan StreamEvent, 1)
	in := SlotChangedV1{
		Slot:         "conversation",
		Change:       SlotChangeSummarized,
		Reasoning:    "Conversation slot exceeded budget; summarized 12 oldest messages.",
		TokensBefore: 52134,
		TokensAfter:  43713,
	}
	if err := EmitSlotChangedEvent(ch, in); err != nil {
		t.Fatalf("emit: %v", err)
	}
	ev := <-ch
	if ev.Type != SlotChangedKind {
		t.Errorf("type = %q, want %q", ev.Type, SlotChangedKind)
	}
	var got SlotChangedV1
	if err := json.Unmarshal([]byte(ev.Envelope), &got); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if got.V != SlotChangedV1Version {
		t.Errorf("default V missing: got %d, want %d", got.V, SlotChangedV1Version)
	}
	if got.HelpLink != SlotChangedDefaultHelpLink {
		t.Errorf("default HelpLink missing: got %q", got.HelpLink)
	}
	if got.Slot != "conversation" || got.Change != SlotChangeSummarized {
		t.Errorf("payload round-trip mismatch: %+v", got)
	}
}

func TestEmitSlotChangedEvent_NilStreamIsNoOp(t *testing.T) {
	if err := EmitSlotChangedEvent(nil, SlotChangedV1{Slot: "memory"}); err != nil {
		t.Errorf("nil stream should be no-op, got %v", err)
	}
}

func TestSlotChangedResponseHandler_Registered(t *testing.T) {
	h := LookupResponseHandler(SlotChangedKind)
	if _, ok := h.(slotChangedResponseHandler); !ok {
		t.Errorf("slot_changed handler not registered, got %T", h)
	}
}
