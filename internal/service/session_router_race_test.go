package service

import (
	"sync"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// Regression coverage for CW-20260824-0001 — the sessionRouter
// send-on-closed-channel race.
//
// Before the fix, both production senders did a check-then-act TOCTOU:
//
//	if !router.closed.Load() {
//	    select {
//	    case router.ch <- ev:   // <- closeOnce() may have closed ch by now
//	    default:
//	    }
//	}
//
// `closed.Load()` returning false only says the router was open at that
// instant. closeOnce() can CompareAndSwap + close(r.ch) between the load and
// the send, which is both a data race (chansend vs. closeChan on the same
// hchan) and a real `send on closed channel` panic. The fix moves the closed
// check inside a RWMutex that closeOnce() takes for write, so the check and
// the send are atomic with respect to the close.
//
// Both tests below are race-detector tests — run them under `-race`. Measured
// on the pre-fix tree at d5de0ba1: under `-race` the fanout test below fails on
// the first round; without `-race` it passed 20 consecutive runs, which is
// exactly why the underlying bug reads as an intermittent nightly-CI flake
// rather than as a reproducible defect.

// TestSessionRouterSendRacesCloseOnce hammers the guarded send directly
// against concurrent closeOnce calls. Verified by temporarily reducing send()
// to the pre-fix check-then-act (read `closed`, release the lock, then send):
// under `-race` that reports a WARNING: DATA RACE between chansend and
// closeChan and also panics with "send on closed channel".
func TestSessionRouterSendRacesCloseOnce(t *testing.T) {
	const (
		rounds  = 300
		senders = 4
	)

	for i := 0; i < rounds; i++ {
		// Unbuffered: every send that gets past the closed check parks in
		// chansend, which is the widest possible window against close().
		// A drainer keeps some of them completing rather than all taking
		// the non-blocking `default` branch.
		r := &sessionRouter{ch: make(chan llmtypes.StreamEvent)}

		drained := make(chan struct{})
		go func() {
			for range r.ch { //nolint:revive // draining until close
			}
			close(drained)
		}()

		var wg sync.WaitGroup
		start := make(chan struct{})

		for s := 0; s < senders; s++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for j := 0; j < 20; j++ {
					r.send(llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: "x"})
				}
			}()
		}

		// Two closers so closeOnce's own idempotence is exercised too.
		for c := 0; c < 2; c++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				r.closeOnce()
			}()
		}

		close(start)
		wg.Wait()
		<-drained
	}
}

// TestAgentEventBridgeFanoutRacesRouterUnbind exercises the same race through
// the real production interleaving: the bridge fanout goroutine forwarding
// runtime events into a bound per-turn chan (site 2, agent_deps.go) while the
// chat-harness ctx-cancel watcher concurrently unbinds the router via
// SetPerSessionRouter(sessionID, nil) — which is what closes the chan.
//
// This is the interleaving 13/03's race gate caught in the wild. Unlike the
// test above it compiles and runs unchanged against the pre-fix tree, so it
// is the load-bearing "would have caught it" case.
func TestAgentEventBridgeFanoutRacesRouterUnbind(t *testing.T) {
	const rounds = 300

	for i := 0; i < rounds; i++ {
		bridge := &agentEventBridge{streams: NewStreamManager()}
		const sessionID = "sess-race"

		in := bridge.fanout(sessionID)

		// Unbuffered turnCh so the fanout's send actually parks in chansend
		// instead of dropping straight to `default`.
		turnCh := make(chan llmtypes.StreamEvent)
		bridge.SetPerSessionRouter(sessionID, turnCh)

		drained := make(chan struct{})
		go func() {
			for range turnCh { //nolint:revive // draining until close
			}
			close(drained)
		}()

		var wg sync.WaitGroup
		start := make(chan struct{})

		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 20; j++ {
				in <- llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: "x"}
			}
			close(in)
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			bridge.SetPerSessionRouter(sessionID, nil)
		}()

		close(start)
		wg.Wait()
		<-drained
	}
}
