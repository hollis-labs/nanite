package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/tool/intent"
)

func drainOne(t *testing.T, ch chan chat.StreamEvent) *chat.StreamEvent {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			return nil
		}
		return &ev
	case <-time.After(100 * time.Millisecond):
		return nil
	}
}

func TestEmitToolSlotChanged_PointerToFullEmitsHydrated(t *testing.T) {
	ch := make(chan chat.StreamEvent, 4)
	outcome := &ToolCacheOutcome{
		Prev:         StatePointer,
		Next:         StateFull,
		Reasoning:    "Detected run intent",
		TokensBefore: 300,
		TokensAfter:  4800,
		Source:       intent.SourceRules,
	}
	emitToolSlotChangedIfNeeded(ch, outcome)

	ev := drainOne(t, ch)
	if ev == nil {
		t.Fatal("expected envelope emitted")
	}
	if ev.Type != chat.SlotChangedKind {
		t.Fatalf("type: got %q, want %q", ev.Type, chat.SlotChangedKind)
	}
	var payload chat.SlotChangedV1
	if err := json.Unmarshal([]byte(ev.Envelope), &payload); err != nil {
		t.Fatalf("envelope not valid JSON: %v", err)
	}
	if payload.Slot != "tools" {
		t.Errorf("slot: got %q, want %q", payload.Slot, "tools")
	}
	if payload.Change != chat.SlotChangeHydrated {
		t.Errorf("change: got %q, want %q", payload.Change, chat.SlotChangeHydrated)
	}
	if payload.TokensBefore != 300 || payload.TokensAfter != 4800 {
		t.Errorf("token counts: got %d→%d", payload.TokensBefore, payload.TokensAfter)
	}
	if !strings.HasSuffix(payload.HelpLink, "#tools") {
		t.Errorf("help link should deep-link to tools section; got %q", payload.HelpLink)
	}
}

func TestEmitToolSlotChanged_FullToPointerEmitsDehydrated(t *testing.T) {
	ch := make(chan chat.StreamEvent, 4)
	emitToolSlotChangedIfNeeded(ch, &ToolCacheOutcome{
		Prev:         StateFull,
		Next:         StatePointer,
		Reasoning:    "Follow-up chit-chat",
		TokensBefore: 4800,
		TokensAfter:  300,
	})
	ev := drainOne(t, ch)
	if ev == nil {
		t.Fatal("expected envelope")
	}
	var payload chat.SlotChangedV1
	_ = json.Unmarshal([]byte(ev.Envelope), &payload)
	if payload.Change != chat.SlotChangeDehydrated {
		t.Fatalf("change: got %q, want %q", payload.Change, chat.SlotChangeDehydrated)
	}
}

func TestEmitToolSlotChanged_PartialIncludesCategories(t *testing.T) {
	ch := make(chan chat.StreamEvent, 4)
	emitToolSlotChangedIfNeeded(ch, &ToolCacheOutcome{
		Prev:       StatePointer,
		Next:       StatePartial,
		Categories: []string{"search"},
		Reasoning:  "User asked to find",
	})
	ev := drainOne(t, ch)
	if ev == nil {
		t.Fatal("expected envelope")
	}
	var payload chat.SlotChangedV1
	_ = json.Unmarshal([]byte(ev.Envelope), &payload)
	if payload.Change != chat.SlotChangePartialHydrated {
		t.Fatalf("change: got %q", payload.Change)
	}
	if len(payload.Categories) != 1 || payload.Categories[0] != "search" {
		t.Fatalf("categories: got %v", payload.Categories)
	}
}

func TestEmitToolSlotChanged_NoTransitionEmitsNothing(t *testing.T) {
	ch := make(chan chat.StreamEvent, 4)
	emitToolSlotChangedIfNeeded(ch, &ToolCacheOutcome{
		Prev: StatePointer,
		Next: StatePointer,
	})
	if ev := drainOne(t, ch); ev != nil {
		t.Fatalf("unexpected envelope for no-transition: %+v", ev)
	}
}

func TestEmitToolSlotChanged_NilOutcomeSafe(t *testing.T) {
	ch := make(chan chat.StreamEvent, 4)
	emitToolSlotChangedIfNeeded(ch, nil)
	if ev := drainOne(t, ch); ev != nil {
		t.Fatalf("nil outcome should not emit: %+v", ev)
	}
}

func TestEmitToolSlotChanged_NilChannelSafe(t *testing.T) {
	// Must not panic.
	emitToolSlotChangedIfNeeded(nil, &ToolCacheOutcome{Prev: StatePointer, Next: StateFull})
}

func TestEmitToolSlotChanged_FullToPartialEmitsPartial(t *testing.T) {
	ch := make(chan chat.StreamEvent, 4)
	emitToolSlotChangedIfNeeded(ch, &ToolCacheOutcome{
		Prev:       StateFull,
		Next:       StatePartial,
		Categories: []string{"code-exec"},
	})
	ev := drainOne(t, ch)
	if ev == nil {
		t.Fatal("expected envelope")
	}
	var payload chat.SlotChangedV1
	_ = json.Unmarshal([]byte(ev.Envelope), &payload)
	if payload.Change != chat.SlotChangePartialHydrated {
		t.Fatalf("change: got %q", payload.Change)
	}
}
