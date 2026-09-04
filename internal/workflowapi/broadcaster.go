package workflowapi

import "sync"

// Broadcaster fans out compatibility events to active SSE subscribers.
type Broadcaster struct {
	mu   sync.RWMutex
	subs []chan Event
}

// NewBroadcaster creates a Broadcaster with no subscribers.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{}
}

// Subscribe registers a buffered event subscriber.
func (b *Broadcaster) Subscribe() chan Event {
	ch := make(chan Event, 64)
	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes and closes a subscriber channel.
func (b *Broadcaster) Unsubscribe(ch chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for i, subscriber := range b.subs {
		if subscriber == ch {
			b.subs = append(b.subs[:i], b.subs[i+1:]...)
			close(ch)
			return
		}
	}
}

// Broadcast sends an event without allowing a slow subscriber to block the
// API or other subscribers.
func (b *Broadcaster) Broadcast(event Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, subscriber := range b.subs {
		select {
		case subscriber <- event:
		default:
		}
	}
}
