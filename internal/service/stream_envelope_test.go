package service

import (
	"encoding/json"
	"testing"

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
	ch := sm.CreateStream("msg-1", "sess-1")
	defer sm.CloseStream("msg-1")

	env := sdkplugin.EnvelopeOut{
		Type: "oembed-card",
		Data: map[string]interface{}{"url": "https://example.com/video"},
	}

	ok := sm.DeliverSessionEnvelopes("sess-1", "oembed", []sdkplugin.EnvelopeOut{env})
	if !ok {
		t.Fatalf("DeliverSessionEnvelopes returned false; want true when stream attached")
	}

	select {
	case got := <-ch:
		if got.Type != "plugin_envelope" {
			t.Fatalf("stream event type: want plugin_envelope, got %q", got.Type)
		}
		if got.PluginID != "oembed" {
			t.Errorf("plugin id: want oembed, got %q", got.PluginID)
		}
		var decoded sdkplugin.EnvelopeOut
		if err := json.Unmarshal([]byte(got.Envelope), &decoded); err != nil {
			t.Fatalf("envelope payload is not valid JSON: %v (%s)", err, got.Envelope)
		}
		if decoded.Type != "oembed-card" {
			t.Errorf("envelope inner type: want oembed-card, got %q", decoded.Type)
		}
	default:
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
