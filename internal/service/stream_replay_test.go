package service

import (
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
)

// waitUntil polls cond with a short backoff until it returns true or the
// deadline elapses. Replaces fixed time.Sleep in tests that need to
// observe the pump goroutine catching up. PR #66 review #3.
func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("condition never became true within %s", timeout)
}

// TestMessageStream_AssignsMonotonicEventIDs verifies the pump stamps each
// event with a monotonically increasing EventID starting at 1, and that
// identical event contents on either side of the pump retain the assigned
// ID. CW-20260418-0100.
func TestMessageStream_AssignsMonotonicEventIDs(t *testing.T) {
	sm := NewStreamManager()
	ch := sm.CreateStream("msg-A", "sess-1")

	sub, closed, ok := sm.Subscribe("msg-A", 0)
	if !ok || closed {
		t.Fatalf("expected open subscription, got ok=%v closed=%v", ok, closed)
	}

	ch <- chat.StreamEvent{Type: "delta", Content: "one"}
	ch <- chat.StreamEvent{Type: "delta", Content: "two"}
	ch <- chat.StreamEvent{Type: "delta", Content: "three"}

	got := make([]chat.StreamEvent, 0, 3)
	for i := 0; i < 3; i++ {
		select {
		case evt := <-sub:
			got = append(got, evt)
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("timed out waiting for event %d; got %d so far", i, len(got))
		}
	}

	for i, evt := range got {
		wantID := uint64(i + 1)
		if evt.EventID != wantID {
			t.Errorf("event %d: want EventID=%d, got %d", i, wantID, evt.EventID)
		}
	}
	close(ch)
}

// TestMessageStream_ReplaysAfterCursorReconnect verifies that a subscriber
// which drops and reconnects with a cursor picks up the events it missed.
// This is the heart of CW-20260418-0100 — SSE drops mid-generation no
// longer lose output.
func TestMessageStream_ReplaysAfterCursorReconnect(t *testing.T) {
	sm := NewStreamManager()
	ch := sm.CreateStream("msg-B", "sess-1")

	sub1, _, _ := sm.Subscribe("msg-B", 0)

	// Deliver first three events to sub1.
	ch <- chat.StreamEvent{Type: "delta", Content: "a"}
	ch <- chat.StreamEvent{Type: "delta", Content: "b"}
	ch <- chat.StreamEvent{Type: "delta", Content: "c"}

	var lastSeen uint64
	for i := 0; i < 3; i++ {
		evt := <-sub1
		lastSeen = evt.EventID
	}
	if lastSeen != 3 {
		t.Fatalf("expected lastSeen=3, got %d", lastSeen)
	}

	// Producer keeps writing while sub1 is "disconnected" (no reader).
	ch <- chat.StreamEvent{Type: "delta", Content: "d"}
	ch <- chat.StreamEvent{Type: "delta", Content: "e"}

	// Reconnect with cursor. Expect d,e to replay.
	sub2, closed, ok := sm.Subscribe("msg-B", lastSeen)
	if !ok || closed {
		t.Fatalf("expected open reconnection, got ok=%v closed=%v", ok, closed)
	}
	got := make([]chat.StreamEvent, 0, 2)
	for i := 0; i < 2; i++ {
		select {
		case evt := <-sub2:
			got = append(got, evt)
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("timed out on replay event %d; got %d so far: %+v", i, len(got), got)
		}
	}
	if got[0].Content != "d" || got[0].EventID != 4 {
		t.Errorf("replay[0]: want content=d id=4, got %+v", got[0])
	}
	if got[1].Content != "e" || got[1].EventID != 5 {
		t.Errorf("replay[1]: want content=e id=5, got %+v", got[1])
	}

	close(ch)
}

// TestMessageStream_LiveAfterReplay verifies a reconnecting subscriber
// receives both the replay and subsequent live events without duplicates.
func TestMessageStream_LiveAfterReplay(t *testing.T) {
	sm := NewStreamManager()
	ch := sm.CreateStream("msg-C", "sess-1")

	// Produce 2 events before any subscriber.
	ch <- chat.StreamEvent{Type: "delta", Content: "x"}
	ch <- chat.StreamEvent{Type: "delta", Content: "y"}

	// PR #66 review #3: drain both events via a sync subscription so the
	// pump has deterministically processed them before we take a second
	// subscription to exercise the replay path. The previous version
	// relied on time.Sleep(20ms) which was flaky under CI load.
	syncSub, closed, ok := sm.Subscribe("msg-C", 0)
	if !ok || closed {
		t.Fatalf("sync subscribe: ok=%v closed=%v", ok, closed)
	}
	for i := 0; i < 2; i++ {
		select {
		case evt := <-syncSub:
			if evt.EventID != uint64(i+1) {
				t.Fatalf("sync drain[%d]: want EventID=%d, got %d", i, i+1, evt.EventID)
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("timed out draining sync subscription at event %d", i)
		}
	}

	// Now take the "real" subscription — Subscribe takes over from syncSub,
	// and the buffer is guaranteed to have events 1 and 2 available for replay.
	sub, _, _ := sm.Subscribe("msg-C", 0)

	first := <-sub
	second := <-sub
	if first.Content != "x" || second.Content != "y" {
		t.Fatalf("replay order wrong: %s then %s", first.Content, second.Content)
	}

	// Live event arrives after Subscribe.
	ch <- chat.StreamEvent{Type: "delta", Content: "z"}
	select {
	case evt := <-sub:
		if evt.Content != "z" || evt.EventID != 3 {
			t.Errorf("live event wrong: %+v", evt)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("live event never arrived")
	}

	close(ch)
}

// TestMessageStream_RingEvicts verifies the ring buffer retains at most
// ringBufferCapacity events; a cursor older than the oldest retained event
// only replays what's still in the buffer.
func TestMessageStream_RingEvicts(t *testing.T) {
	sm := NewStreamManager()
	ch := sm.CreateStream("msg-D", "sess-1")

	// Interleave produce + drain so the subscriber channel never fills —
	// the pump's "close slow subscriber on overflow" behavior (PR #66
	// review #5) would otherwise terminate sub1 partway through and the
	// test would never see the final events. Draining synchronously per
	// producer write also replaces the earlier time.Sleep with a
	// deterministic wait (PR #66 review #3).
	sub1, _, _ := sm.Subscribe("msg-D", 0)
	total := ringBufferCapacity*2 + 10
	for i := 0; i < total; i++ {
		ch <- chat.StreamEvent{Type: "delta", Content: "x"}
		select {
		case evt, ok := <-sub1:
			if !ok {
				t.Fatalf("sub1 closed before producing all %d events (at iter=%d)", total, i)
			}
			if evt.EventID != uint64(i+1) {
				t.Fatalf("sub1 drain[%d]: want EventID=%d, got %d", i, i+1, evt.EventID)
			}
		case <-time.After(1 * time.Second):
			t.Fatalf("sub1 drain timed out at iter=%d", i)
		}
	}

	// Reconnect with cursor=0. Replay should yield at most ringBufferCapacity.
	sub2, _, _ := sm.Subscribe("msg-D", 0)
	close(ch)

	count := 0
	timeout := time.After(1 * time.Second)
	for {
		select {
		case _, ok := <-sub2:
			if !ok {
				goto done
			}
			count++
		case <-timeout:
			goto done
		}
	}
done:
	if count > ringBufferCapacity {
		t.Errorf("replay exceeded ring capacity: got %d events, cap=%d", count, ringBufferCapacity)
	}
	if count == 0 {
		t.Errorf("replay yielded zero events; expected up to %d", ringBufferCapacity)
	}
}

// TestMessageStream_SubscribeAfterClose verifies a subscriber connecting
// after the producer closes still gets the buffered replay, then sees the
// channel close (EOF). Important: the frontend watchdog's "reconnect after
// stall" path can land here if the generation completed during the stall
// window.
func TestMessageStream_SubscribeAfterClose(t *testing.T) {
	sm := NewStreamManager()
	ch := sm.CreateStream("msg-E", "sess-1")

	ch <- chat.StreamEvent{Type: "delta", Content: "p"}
	ch <- chat.StreamEvent{Type: "delta", Content: "q"}
	close(ch)
	// Wait for the pump to exit so closed=true is visible to Subscribe.
	// Replaces the earlier time.Sleep(50ms) — PR #66 review #3.
	waitUntil(t, 2*time.Second, func() bool {
		val, ok := sm.streams.Load("msg-E")
		if !ok {
			return false
		}
		ms, ok := val.(*messageStream)
		if !ok {
			return false
		}
		ms.mu.Lock()
		defer ms.mu.Unlock()
		return ms.closed
	})
	time.Sleep(50 * time.Millisecond)

	sub, closed, ok := sm.Subscribe("msg-E", 0)
	if !ok {
		t.Fatal("expected Subscribe to succeed for registered-but-closed stream")
	}
	if !closed {
		t.Error("expected closed=true for post-close subscription")
	}
	// Drain replay.
	first := <-sub
	second := <-sub
	if first.Content != "p" || second.Content != "q" {
		t.Errorf("replay contents wrong: %q then %q", first.Content, second.Content)
	}
	// Channel must be closed.
	if _, ok := <-sub; ok {
		t.Error("expected sub channel to be closed after replay")
	}
}

// TestMessageStream_TakeoverClosesPriorSubscriber verifies that a second
// Subscribe call closes the prior subscriber's channel (so the old SSE
// client sees EOF and can reconnect cleanly), enforcing a single live
// reader per message.
func TestMessageStream_TakeoverClosesPriorSubscriber(t *testing.T) {
	sm := NewStreamManager()
	ch := sm.CreateStream("msg-F", "sess-1")
	defer close(ch)

	sub1, _, _ := sm.Subscribe("msg-F", 0)
	sub2, _, _ := sm.Subscribe("msg-F", 0)

	// sub1 should be closed.
	select {
	case _, ok := <-sub1:
		if ok {
			t.Error("expected sub1 closed after takeover; got an event")
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("sub1 did not close after takeover")
	}

	// sub2 should receive live events.
	ch <- chat.StreamEvent{Type: "delta", Content: "live"}
	select {
	case evt := <-sub2:
		if evt.Content != "live" {
			t.Errorf("unexpected content on sub2: %q", evt.Content)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("sub2 never received live event")
	}
}

// TestMessageStream_NoRaceOnTakeoverDuringFanout exercises the race the
// detector flagged in CW-20260510-0002: pump's non-blocking send to the
// current subscriber must not race subscribe()'s close(prev). A producer
// fires events while several reconnector goroutines repeatedly Subscribe
// and abandon, each Subscribe triggering close(prev) on the previous
// subscriber the pump might be writing to. Under -race, the pre-fix code
// reliably reproduces a write/close data race; the fix serialises both
// paths under ms.mu.
func TestMessageStream_NoRaceOnTakeoverDuringFanout(t *testing.T) {
	sm := NewStreamManager()
	ch := sm.CreateStream("msg-race", "sess-1")

	const events = 500
	const reconnectors = 4
	const reconnectsEach = 100

	var wg sync.WaitGroup

	// Producer: stream events as fast as possible.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < events; i++ {
			ch <- chat.StreamEvent{Type: "delta", Content: "x"}
		}
		close(ch)
	}()

	// Reconnect goroutines: each loop's Subscribe triggers a close(prev)
	// that runs concurrently with the pump's fanout sends.
	for r := 0; r < reconnectors; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < reconnectsEach; i++ {
				sub, closed, ok := sm.Subscribe("msg-race", 0)
				if !ok {
					return
				}
				if closed {
					return
				}
				// Drain a tiny window so the pump pushes events at us
				// before we abandon and the next Subscribe arrives.
				timeout := time.After(200 * time.Microsecond)
			drain:
				for {
					select {
					case _, open := <-sub:
						if !open {
							break drain
						}
					case <-timeout:
						break drain
					}
				}
			}
		}()
	}

	wg.Wait()
}
