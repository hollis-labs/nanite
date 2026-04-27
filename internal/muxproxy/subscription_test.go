package muxproxy

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	agentmux "github.com/hollis-labs/go-agentmux-client"
)

// fakeStream satisfies the streamSource interface. For the POC we
// exercise the attach path: AttachSession writes a scripted NDJSON
// payload to the writer then returns.
type fakeStream struct {
	attachData map[string]string // sessionID → NDJSON body (newline-delimited JSON lines)
}

// StreamEvents is required by the streamSource interface but not
// exercised at runtime anymore — claudestream events ride attach.
func (f *fakeStream) StreamEvents(_ context.Context, _ agentmux.StreamEventsOptions) (<-chan agentmux.StreamEvent, <-chan error) {
	events := make(chan agentmux.StreamEvent)
	errs := make(chan error, 1)
	close(events)
	close(errs)
	return events, errs
}

func (f *fakeStream) AttachSession(_ context.Context, sessionID string, w io.Writer, _ int64) error {
	if body, ok := f.attachData[sessionID]; ok {
		_, _ = io.WriteString(w, body)
	}
	return nil
}

// fakePublisher records PublishSubEvent calls.
type publishedEvent struct {
	sessionID string
	evt       SubEvent
}

type fakePublisher struct {
	mu        sync.Mutex
	published []publishedEvent
}

func (fp *fakePublisher) PublishSubEvent(sessionID string, evt SubEvent) {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	fp.published = append(fp.published, publishedEvent{sessionID: sessionID, evt: evt})
}

func (fp *fakePublisher) snapshot() []publishedEvent {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	out := make([]publishedEvent, len(fp.published))
	copy(out, fp.published)
	return out
}

func TestManager_RegisterAndFanout(t *testing.T) {
	// Scripted subordinate stream: one assistant-text event, then
	// a result event that produces {KindUsage, KindDone}.
	f := &fakeStream{attachData: map[string]string{
		"sess-A": `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}` + "\n" +
			`{"type":"result","subtype":"success","is_error":false,"result":"hi","stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":1}}` + "\n",
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fp := &fakePublisher{}
	m := NewManagerWithStream(f)
	m.SetPublisher(fp)
	go m.Run(ctx)

	ch := m.Register("chat-1", "sess-A", "Alice")

	// Expect the delta to arrive on the waiter channel.
	select {
	case ev := <-ch:
		if ev.Text != "hi" {
			t.Fatalf("want text=hi, got %q", ev.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter channel did not receive delta")
	}

	// Drain remaining events (Usage + Done) from the waiter to avoid
	// the fanout goroutine dropping them on a full buffer.
	drained := 0
	for drained < 3 {
		select {
		case <-ch:
			drained++
		case <-time.After(500 * time.Millisecond):
			drained = 3 // give up; some may have been dropped
		}
	}

	// Give dispatch a moment to call the publisher for each event.
	time.Sleep(50 * time.Millisecond)

	pub := fp.snapshot()
	if len(pub) == 0 {
		t.Fatal("publisher received no events")
	}

	// Find the delta.
	var foundDelta bool
	for _, pe := range pub {
		if pe.sessionID != "chat-1" {
			t.Fatalf("want sessionID=chat-1, got %q", pe.sessionID)
		}
		if pe.evt.Type == "subordinate_delta" && pe.evt.Content == "hi" && pe.evt.AgentID == "Alice" {
			foundDelta = true
		}
	}
	if !foundDelta {
		t.Fatalf("no subordinate_delta found in %+v", pub)
	}
}

func TestManager_IgnoresUnregisteredSessions(t *testing.T) {
	f := &fakeStream{attachData: map[string]string{
		"sess-A": `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"ignore me"}]}}` + "\n",
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fp := &fakePublisher{}
	m := NewManagerWithStream(f)
	m.SetPublisher(fp)
	go m.Run(ctx)

	// Do NOT Register sess-A. Manager should never attach an
	// orphan session.
	time.Sleep(100 * time.Millisecond)

	if pub := fp.snapshot(); len(pub) != 0 {
		t.Fatalf("publisher received event for unregistered session: %+v", pub)
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
