package plugin

import (
	pluginsdk "github.com/hollis-labs/fragments-engine/plugin"
)

// SubscribeEvents creates a buffered channel that receives all emitted events.
// The caller must call UnsubscribeEvents when done to avoid leaking goroutines.
func (h *Host) SubscribeEvents() chan pluginsdk.Event {
	ch := make(chan pluginsdk.Event, 64)
	h.mu.Lock()
	h.eventSubs = append(h.eventSubs, ch)
	h.mu.Unlock()
	return ch
}

// UnsubscribeEvents removes a subscriber channel and closes it.
func (h *Host) UnsubscribeEvents(ch chan pluginsdk.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, sub := range h.eventSubs {
		if sub == ch {
			h.eventSubs = append(h.eventSubs[:i], h.eventSubs[i+1:]...)
			close(ch)
			return
		}
	}
}

// broadcastEvent sends an event to all SSE subscribers. Non-blocking — if a
// subscriber's buffer is full the event is dropped for that subscriber.
func (h *Host) broadcastEvent(event pluginsdk.Event) {
	h.mu.RLock()
	subs := make([]chan pluginsdk.Event, len(h.eventSubs))
	copy(subs, h.eventSubs)
	h.mu.RUnlock()

	for _, ch := range subs {
		select {
		case ch <- event:
		default:
			// Subscriber buffer full — drop event to avoid blocking.
		}
	}
}
