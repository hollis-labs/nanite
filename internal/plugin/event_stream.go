package plugin

import (
	pluginsdk "github.com/hollis-labs/plugin"
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

// UnsubscribeEvents removes a subscriber channel. The channel is NOT closed
// here to avoid send-to-closed-channel panics in broadcastEvent; the caller
// should drain and discard any remaining events after unsubscribing.
func (h *Host) UnsubscribeEvents(ch chan pluginsdk.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, sub := range h.eventSubs {
		if sub == ch {
			h.eventSubs = append(h.eventSubs[:i], h.eventSubs[i+1:]...)
			return
		}
	}
}

// broadcastEvent sends an event to all SSE subscribers. Non-blocking — if a
// subscriber's buffer is full the event is dropped for that subscriber.
// Holds the read lock for the duration of sends to prevent races with
// concurrent unsubscribe operations.
func (h *Host) broadcastEvent(event pluginsdk.Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, ch := range h.eventSubs {
		select {
		case ch <- event:
		default:
			// Subscriber buffer full — drop event to avoid blocking.
		}
	}
}
