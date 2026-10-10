package service

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

func assertMessageStreamOutcome(t *testing.T, events []chat.StreamEvent, outcome string) {
	t.Helper()
	var terminal *chat.StreamEvent
	for i := range events {
		if events[i].Type == "stream_end" {
			if terminal != nil {
				t.Fatalf("duplicate producer terminal: %+v", events)
			}
			terminal = &events[i]
		}
	}
	if terminal == nil || terminal.Termination == nil || terminal.Termination.Outcome != outcome {
		t.Fatalf("terminal=%+v, want outcome %s; events=%+v", terminal, outcome, events)
	}
	if len(events) == 0 || events[len(events)-1].Type != "stream_end" {
		t.Fatalf("events followed the terminal: %+v", events)
	}
}

func TestMessageStreamProducerCancellationDeclaresOutcome(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f := newCharacterizationFixture(t, []characterizationProviderStep{{events: doneEvents("partial"), beforeReturn: cancel}})
	events := f.runCtx(ctx, t, "canceled-message")
	assertMessageStreamOutcome(t, events, "canceled")
	if findEvent(events, "error") != nil {
		t.Fatalf("cancellation became a provider error: %+v", events)
	}
}

func TestMessageStreamRetainedFallbackDoesNotCompleteCanonicalRun(t *testing.T) {
	for _, outcome := range []string{"error", "canceled", "success"} {
		t.Run(outcome, func(t *testing.T) {
			turns := NewCognitiveTurns()
			run := turns.create("view", "turn", "fixture", "model", chat.DeltaModeLive, "normal", func() (*store.Message, error) {
				return &store.Message{ID: "turn", SessionID: "view", Role: "assistant", Content: "answer"}, nil
			})
			streams := NewStreamManager()
			producer := streams.createCognitiveStream("turn", "view", run)
			consumer, _, _ := streams.Subscribe("turn", 0)
			producer <- chat.StreamEvent{Type: "stream_start"}
			producer <- chat.StreamEvent{Type: "delta", Content: "answer", Phase: chat.PhaseFinal}
			if outcome == "canceled" {
				turns.Ending("turn", true)
			}
			producer <- chat.StreamEvent{Type: "stream_end", Termination: &chat.StreamTermination{Reason: "fixture", Outcome: outcome}, RetainedTerminal: outcome != "success"}
			close(producer)
			assertMessageStreamOutcome(t, drainStream(consumer), outcome)
			snapshot, err := turns.Get("view", "turn")
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"error": "failed", "canceled": "canceled", "success": "completed"}[outcome]
			if snapshot.State != want {
				t.Fatalf("retained marker changed canonical outcome: got %s want %s", snapshot.State, want)
			}
		})
	}
}

func TestMessageStreamObserverDetachDoesNotDetachReplacement(t *testing.T) {
	streams := NewStreamManager()
	producer := streams.CreateStream("message", "session")
	defer close(producer)
	old, ok := streams.SubscribeSSETransport("message", 0)
	if !ok {
		t.Fatal("missing first observer")
	}
	next, ok := streams.SubscribeSSETransport("message", 0)
	if !ok {
		t.Fatal("missing replacement")
	}
	defer next.Cancel()
	old.Cancel()
	producer <- chat.StreamEvent{Type: "delta", Content: "still running"}
	select {
	case event := <-next.Events:
		if event.Content != "still running" {
			t.Fatalf("replacement event=%+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("old observer detached its replacement")
	}
	if end := <-old.Closure; end.Reason != "session_takeover" || end.Outcome != "" {
		t.Fatalf("takeover altered producer outcome: %+v", end)
	}
	if streams.ActiveMessageForSession("session") != "message" {
		t.Fatal("observer detach canceled execution")
	}
}
