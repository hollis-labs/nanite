package muxproxy

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	agentmux "github.com/hollis-labs/go-agentmux-client"
)

// fakeStream satisfies the minimal surface the Manager consumes from
// the agent-mux client: StreamEvents-shaped output via injected channels.
type fakeStream struct {
	events chan agentmux.StreamEvent
	errs   chan error
}

func (f *fakeStream) StreamEvents(ctx context.Context, _ agentmux.StreamEventsOptions) (<-chan agentmux.StreamEvent, <-chan error) {
	return f.events, f.errs
}

func TestManager_RegisterAndFanout(t *testing.T) {
	f := &fakeStream{
		events: make(chan agentmux.StreamEvent, 4),
		errs:   make(chan error, 1),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sink := make(chan SubordinateStreamEvent, 16)
	m := NewManagerWithStream(f)
	ch := m.Register("sess-A", "Alice")

	go m.Run(ctx, sink)

	payload := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`
	f.events <- agentmux.StreamEvent{SessionID: "sess-A", PayloadJSON: payload}

	select {
	case ev := <-ch:
		if ev.Text != "hi" {
			t.Fatalf("want text=hi, got %q", ev.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter channel did not receive event")
	}

	select {
	case sev := <-sink:
		if sev.Nickname != "Alice" || sev.Type != "subordinate_delta" {
			t.Fatalf("unexpected sink event: %+v", sev)
		}
	case <-time.After(time.Second):
		t.Fatal("sink did not receive live-render event")
	}
	_ = json.RawMessage{}
}

func TestManager_IgnoresUnregisteredSessions(t *testing.T) {
	f := &fakeStream{
		events: make(chan agentmux.StreamEvent, 2),
		errs:   make(chan error, 1),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sink := make(chan SubordinateStreamEvent, 4)
	m := NewManagerWithStream(f)
	go m.Run(ctx, sink)

	payload := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"ignore me"}]}}`
	f.events <- agentmux.StreamEvent{SessionID: "not-ours", PayloadJSON: payload}

	select {
	case sev := <-sink:
		t.Fatalf("sink received event for unregistered session: %+v", sev)
	case <-time.After(100 * time.Millisecond):
		// good — no event delivered
	}
}
