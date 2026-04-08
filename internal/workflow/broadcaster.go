package workflow

import "sync"

// Broadcaster fans out workflow events to all active SSE subscribers.
type Broadcaster struct {
	mu   sync.RWMutex
	subs []chan Event
}

// NewBroadcaster creates a new Broadcaster with no subscribers.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{}
}

// Subscribe registers a new subscriber and returns a buffered event channel.
func (b *Broadcaster) Subscribe() chan Event {
	ch := make(chan Event, 64)
	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber channel and closes it.
func (b *Broadcaster) Unsubscribe(ch chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for i, sub := range b.subs {
		if sub == ch {
			b.subs = append(b.subs[:i], b.subs[i+1:]...)
			close(ch)
			return
		}
	}
}

// Broadcast sends an event to all subscribers. The send is non-blocking —
// slow or full subscribers are skipped silently.
func (b *Broadcaster) Broadcast(event Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, ch := range b.subs {
		select {
		case ch <- event:
		default:
		}
	}
}
