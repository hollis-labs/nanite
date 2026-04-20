package muxproxy

import (
	"context"
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

// fakePublisher records PublishSubEvent calls.
type publishedEvent struct {
	sessionID string
	evt       SubEvent
}

type fakePublisher struct {
	published []publishedEvent
}

func (fp *fakePublisher) PublishSubEvent(sessionID string, evt SubEvent) {
	fp.published = append(fp.published, publishedEvent{sessionID: sessionID, evt: evt})
}

func TestManager_RegisterAndFanout(t *testing.T) {
	f := &fakeStream{
		events: make(chan agentmux.StreamEvent, 4),
		errs:   make(chan error, 1),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fp := &fakePublisher{}
	m := NewManagerWithStream(f)
	m.SetPublisher(fp)
	ch := m.Register("chat-1", "sess-A", "Alice")

	go m.Run(ctx)

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

	// Give dispatch a moment to call the publisher.
	time.Sleep(20 * time.Millisecond)

	if len(fp.published) == 0 {
		t.Fatal("publisher received no events")
	}
	got := fp.published[0]
	if got.sessionID != "chat-1" {
		t.Fatalf("want sessionID=chat-1, got %q", got.sessionID)
	}
	if got.evt.Type != "subordinate_delta" {
		t.Fatalf("want type=subordinate_delta, got %q", got.evt.Type)
	}
	if got.evt.Content != "hi" {
		t.Fatalf("want content=hi, got %q", got.evt.Content)
	}
	if got.evt.AgentID != "Alice" {
		t.Fatalf("want agent_id=Alice, got %q", got.evt.AgentID)
	}
}

func TestManager_StopAllForChat(t *testing.T) {
	mgr := NewManagerWithStream(nil)
	mgr.Register("chat-1", "sess-A", "Alice")
	mgr.Register("chat-1", "sess-B", "Bob")
	mgr.Register("chat-2", "sess-C", "Charlie")

	stopped := mgr.StopAllForChat("chat-1")
	if len(stopped) != 2 {
		t.Fatalf("want 2 stopped, got %d (%v)", len(stopped), stopped)
	}
	if mgr.Nickname("sess-A") != "" || mgr.Nickname("sess-B") != "" {
		t.Fatal("sess-A/B should be cleared")
	}
	if mgr.Nickname("sess-C") == "" {
		t.Fatal("sess-C should still be registered")
	}
}

func TestManager_StopAll(t *testing.T) {
	mgr := NewManagerWithStream(nil)
	mgr.Register("chat-1", "sess-A", "Alice")
	mgr.Register("chat-2", "sess-C", "Charlie")

	all := mgr.StopAll()
	if len(all) != 2 {
		t.Fatalf("want 2 stopped, got %d (%v)", len(all), all)
	}
	if mgr.Nickname("sess-A") != "" || mgr.Nickname("sess-C") != "" {
		t.Fatal("all nicknames should be cleared")
	}
}

func TestManager_IgnoresUnregisteredSessions(t *testing.T) {
	f := &fakeStream{
		events: make(chan agentmux.StreamEvent, 2),
		errs:   make(chan error, 1),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fp := &fakePublisher{}
	m := NewManagerWithStream(f)
	m.SetPublisher(fp)
	go m.Run(ctx)

	payload := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"ignore me"}]}}`
	f.events <- agentmux.StreamEvent{SessionID: "not-ours", PayloadJSON: payload}

	time.Sleep(100 * time.Millisecond)

	if len(fp.published) != 0 {
		t.Fatalf("publisher received event for unregistered session: %+v", fp.published)
	}
}
