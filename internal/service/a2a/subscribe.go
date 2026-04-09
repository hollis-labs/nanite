// Package a2a — subscribe.go
//
// In-process pubsub used by Service.SendMessage to fan out newly-inserted
// messages to live MCP streaming subscribers. There is no replay buffer:
// a subscriber only sees messages published after its subscribe call
// takes effect, and subscribers that cannot keep up with their channel
// buffer are dropped silently (the publish is non-blocking).
//
// Concurrency model:
//
//   - subscribe appends a buffered channel to the per-key slice under a
//     write lock, then spawns a goroutine that waits on ctx.Done() to
//     remove and close the channel under a write lock.
//   - publish snapshots the current subscriber slice under an RLock and
//     releases the lock before performing non-blocking sends. Sending
//     outside the lock means publish never holds the pubsub lock while
//     touching a channel, which keeps it from interfering with
//     unsubscribe's write-lock path.
package a2a

import (
	"context"
	"sync"

	"github.com/hollis-labs/nanite/internal/store"
)

// subscriberBufferSize is the capacity of each subscriber's channel. 16
// is enough to absorb small bursts without dropping, but small enough
// that a wedged consumer is noticed quickly.
const subscriberBufferSize = 16

// pubsub is a minimal in-process fan-out hub keyed by
// sessionID + ":" + agentID. It is safe for concurrent use.
type pubsub struct {
	mu   sync.RWMutex
	subs map[string][]chan *store.A2AMessage
}

// newPubsub constructs an empty pubsub.
func newPubsub() *pubsub {
	return &pubsub{subs: make(map[string][]chan *store.A2AMessage)}
}

// subscribe registers a new subscriber for the given (sessionID, agentID)
// pair and returns the receive side of a buffered channel. The caller is
// expected to cancel ctx when the subscription should end; a goroutine
// owned by the pubsub then removes the channel from the subscriber table
// and closes it so that pending receivers unblock.
func (p *pubsub) subscribe(ctx context.Context, sessionID, agentID string) <-chan *store.A2AMessage {
	key := sessionID + ":" + agentID
	ch := make(chan *store.A2AMessage, subscriberBufferSize)

	p.mu.Lock()
	p.subs[key] = append(p.subs[key], ch)
	p.mu.Unlock()

	go func() {
		<-ctx.Done()
		p.mu.Lock()
		defer p.mu.Unlock()
		list := p.subs[key]
		for i, c := range list {
			if c == ch {
				p.subs[key] = append(list[:i], list[i+1:]...)
				break
			}
		}
		if len(p.subs[key]) == 0 {
			delete(p.subs, key)
		}
		close(ch)
	}()

	return ch
}

// publish fans the message out to every subscriber whose key matches the
// message's destination (ToSessionID, ToAgentID). The send is
// non-blocking: if a subscriber's channel is full, the message is
// dropped for that subscriber only.
//
// Per the design in the Task 7 plan, we snapshot the subscriber slice
// under the RLock and release the lock before performing the sends.
// Keeping the sends outside the lock means publish never competes with
// unsubscribe's write lock, and a wedged consumer can never hold up any
// other part of the service.
func (p *pubsub) publish(msg *store.A2AMessage) {
	if msg == nil {
		return
	}
	key := msg.ToSessionID + ":" + msg.ToAgentID

	p.mu.RLock()
	// Copy the slice while holding the RLock so the snapshot is stable
	// with respect to subscribe/unsubscribe mutations.
	subs := make([]chan *store.A2AMessage, len(p.subs[key]))
	copy(subs, p.subs[key])
	p.mu.RUnlock()

	for _, ch := range subs {
		select {
		case ch <- msg:
		default:
			// Subscriber buffer full: drop for this subscriber only.
		}
	}
}

// SubscribeSessionAgent validates the requested agent ID and returns a
// receive-only channel on which live messages addressed to
// (sessionID, agentID) will arrive. The caller must cancel ctx to
// release the subscription; doing so unblocks any pending receive with
// a closed-channel signal.
func (svc *Service) SubscribeSessionAgent(ctx context.Context, sessionID, agentID string) (<-chan *store.A2AMessage, error) {
	if err := ValidateAgentID(ctx, svc.resolver, agentID); err != nil {
		return nil, err
	}
	return svc.pub.subscribe(ctx, sessionID, agentID), nil
}
