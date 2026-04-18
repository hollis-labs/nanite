package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	sdkplugin "github.com/hollis-labs/plugin-sdk"
)

// TestStreamManager_DeliverSessionEnvelopes_ActiveStream covers the happy
// path for BLG-20260413-012: when an active chat SSE stream exists for the
// target session, every envelope delivered through DeliverSessionEnvelopes
// lands on that stream as a plugin_envelope StreamEvent carrying the plugin
// id and a JSON-serialized envelope payload.
func TestStreamManager_DeliverSessionEnvelopes_ActiveStream(t *testing.T) {
	sm := NewStreamManager()
	produce := sm.CreateStream("msg-1", "sess-1")
	defer sm.CloseStream("msg-1")
	defer close(produce)

	// CW-20260418-0100: CreateStream now returns the producer channel; the
	// consumer side is obtained via Subscribe. The plugin-envelope delivery
	// path writes to the producer, the pump assigns EventID + rings, and
	// the subscriber receives the event.
	sub, _, ok := sm.Subscribe("msg-1", 0)
	if !ok {
		t.Fatal("Subscribe returned ok=false for freshly-created stream")
	}

	env := sdkplugin.EnvelopeOut{
		Type: "oembed-card",
		Data: map[string]interface{}{"url": "https://example.com/video"},
	}

	if ok := sm.DeliverSessionEnvelopes("sess-1", "oembed", []sdkplugin.EnvelopeOut{env}); !ok {
		t.Fatalf("DeliverSessionEnvelopes returned false; want true when stream attached")
	}

	select {
	case got := <-sub:
		if got.Type != "plugin_envelope" {
			t.Fatalf("stream event type: want plugin_envelope, got %q", got.Type)
		}
		if got.PluginID != "oembed" {
			t.Errorf("plugin id: want oembed, got %q", got.PluginID)
		}
		if got.EventID == 0 {
			t.Errorf("EventID: want non-zero (ring-buffer assigned), got 0")
		}
		var decoded sdkplugin.EnvelopeOut
		if err := json.Unmarshal([]byte(got.Envelope), &decoded); err != nil {
			t.Fatalf("envelope payload is not valid JSON: %v (%s)", err, got.Envelope)
		}
		if decoded.Type != "oembed-card" {
			t.Errorf("envelope inner type: want oembed-card, got %q", decoded.Type)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("no event on the chat stream — delivery did not reach SSE consumer")
	}

	if drops := sm.PluginEnvelopeDropCount(); drops != 0 {
		t.Errorf("drop count on happy path: want 0, got %d", drops)
	}
}

// TestStreamManager_DeliverSessionEnvelopes_NoActiveStream asserts the
// backpressure contract: when nothing is subscribed for a session, delivery
// returns false and the drop counter advances by the number of envelopes.
func TestStreamManager_DeliverSessionEnvelopes_NoActiveStream(t *testing.T) {
	sm := NewStreamManager()

	envs := []sdkplugin.EnvelopeOut{
		{Type: "a", Data: map[string]interface{}{}},
		{Type: "b", Data: map[string]interface{}{}},
	}
	ok := sm.DeliverSessionEnvelopes("ghost-session", "oembed", envs)
	if ok {
		t.Fatalf("want false when no stream, got true")
	}
	if got := sm.PluginEnvelopeDropCount(); got != 2 {
		t.Errorf("drop count: want 2, got %d", got)
	}
}

// TestStreamManager_DeliverSessionEnvelopes_CloseRemovesSession guarantees
// that once the owning message stream closes, the session-scoped reverse
// index forgets it — subsequent deliveries count as drops rather than
// silently writing into a closed channel.
func TestStreamManager_DeliverSessionEnvelopes_CloseRemovesSession(t *testing.T) {
	sm := NewStreamManager()
	_ = sm.CreateStream("msg-1", "sess-1")
	sm.CloseStream("msg-1")

	env := sdkplugin.EnvelopeOut{Type: "x", Data: map[string]interface{}{}}
	ok := sm.DeliverSessionEnvelopes("sess-1", "p", []sdkplugin.EnvelopeOut{env})
	if ok {
		t.Fatal("CloseStream did not remove session from reverse index")
	}
	if sm.PluginEnvelopeDropCount() != 1 {
		t.Errorf("drop count: want 1, got %d", sm.PluginEnvelopeDropCount())
	}
}

// TestStreamManager_DeliverSessionEnvelopes_ClosedChannelRecovers exercises
// the close/delete race Copilot flagged on PR #36: a producer in
// generateResponse closes the channel before calling CloseStream, so a
// concurrent Deliver can observe an entry in sm.streams whose channel has
// already been closed. trySendEnvelope must recover from the panic and
// count the event as a drop rather than crash the host event dispatcher.
func TestStreamManager_DeliverSessionEnvelopes_ClosedChannelRecovers(t *testing.T) {
	sm := NewStreamManager()
	ch := sm.CreateStream("msg-1", "sess-1")
	defer sm.CloseStream("msg-1")

	// Simulate the producer half of the race: the channel is closed but
	// CloseStream has not yet run, so sm.streams still holds the reference.
	close(ch)

	env := sdkplugin.EnvelopeOut{Type: "oembed-card", Data: map[string]interface{}{}}
	ok := sm.DeliverSessionEnvelopes("sess-1", "oembed", []sdkplugin.EnvelopeOut{env})
	if ok {
		t.Fatal("want false when channel closed; got true")
	}
	if sm.PluginEnvelopeDropCount() != 1 {
		t.Errorf("drop count: want 1, got %d", sm.PluginEnvelopeDropCount())
	}
}

// TestStreamManager_DeliverSessionEnvelopes_SlowConsumerDrops exercises the
// "chat stream full" branch. We fill the buffered channel, then attempt to
// deliver — the event cannot land, so it is counted as a drop. The active
// stream itself is not torn down.
func TestStreamManager_DeliverSessionEnvelopes_SlowConsumerDrops(t *testing.T) {
	sm := NewStreamManager()
	ch := sm.CreateStream("msg-1", "sess-1")
	defer sm.CloseStream("msg-1")

	// Fill the buffered channel (capacity 128).
	for i := 0; i < cap(ch); i++ {
		ch <- chat.StreamEvent{Type: "delta", Content: "x"}
	}

	env := sdkplugin.EnvelopeOut{Type: "oembed-card", Data: map[string]interface{}{}}
	ok := sm.DeliverSessionEnvelopes("sess-1", "oembed", []sdkplugin.EnvelopeOut{env})
	if ok {
		t.Fatal("want false when stream full; got true")
	}
	if sm.PluginEnvelopeDropCount() != 1 {
		t.Errorf("drop count: want 1, got %d", sm.PluginEnvelopeDropCount())
	}
}
