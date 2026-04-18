package provider

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestPacingWait_EmitsImmediateAndHeartbeat verifies the helper fires an
// initial status event and keeps the stream warm with periodic heartbeats
// during longer waits. The important property for the frontend stall
// watchdog is that onStatus is called at least once well under 60s.
// CW-20260418-0043.
func TestPacingWait_EmitsImmediateAndHeartbeat(t *testing.T) {
	var calls atomic.Int32
	done := make(chan struct{})
	go func() {
		_ = PacingWait(context.Background(), 120*time.Millisecond, func(msg string) {
			calls.Add(1)
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("PacingWait did not return")
	}
	if got := calls.Load(); got < 1 {
		t.Fatalf("expected at least one onStatus call, got %d", got)
	}
}

// TestPacingWait_ReturnsOnContextCancel verifies a cancelled ctx unblocks
// the wait immediately, with ctx.Err propagated — needed so the per-session
// takeover cancel (CW-20260418-0043 fix #2) actually aborts in-flight
// provider waits.
func TestPacingWait_ReturnsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- PacingWait(ctx, 5*time.Second, nil)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected non-nil error after cancel")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("PacingWait did not return after cancel")
	}
}

// TestPacingWait_ZeroDurationNoops verifies that a zero/negative wait is a
// no-op and does not call onStatus. Guards against accidental status events
// when the rate tracker reports no pacing needed.
func TestPacingWait_ZeroDurationNoops(t *testing.T) {
	var called atomic.Bool
	if err := PacingWait(context.Background(), 0, func(string) { called.Store(true) }); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if called.Load() {
		t.Fatal("onStatus should not have been called for zero wait")
	}
}

func TestTokenRateTracker_Available_FullBudget(t *testing.T) {
	tr := NewTokenRateTracker(30000)
	if avail := tr.Available(); avail != 30000 {
		t.Errorf("expected 30000 available, got %d", avail)
	}
}

func TestTokenRateTracker_Available_AfterRecord(t *testing.T) {
	tr := NewTokenRateTracker(30000)
	tr.Record(10000)
	if avail := tr.Available(); avail != 20000 {
		t.Errorf("expected 20000 available, got %d", avail)
	}
}

func TestTokenRateTracker_Available_NeverNegative(t *testing.T) {
	tr := NewTokenRateTracker(1000)
	tr.Record(2000)
	if avail := tr.Available(); avail != 0 {
		t.Errorf("expected 0 available, got %d", avail)
	}
}

func TestTokenRateTracker_SlidingWindowExpiry(t *testing.T) {
	tr := NewTokenRateTracker(30000)

	// Inject an old record directly (61 seconds ago).
	tr.mu.Lock()
	tr.window = append(tr.window, tokenRecord{
		at:     time.Now().Add(-61 * time.Second),
		tokens: 20000,
	})
	tr.mu.Unlock()

	// The old record should be expired.
	if avail := tr.Available(); avail != 30000 {
		t.Errorf("expected 30000 after expiry, got %d", avail)
	}
}

func TestTokenRateTracker_SlidingWindowPartialExpiry(t *testing.T) {
	tr := NewTokenRateTracker(30000)

	tr.mu.Lock()
	// Old record: expired.
	tr.window = append(tr.window, tokenRecord{
		at:     time.Now().Add(-61 * time.Second),
		tokens: 15000,
	})
	// Recent record: still active.
	tr.window = append(tr.window, tokenRecord{
		at:     time.Now().Add(-10 * time.Second),
		tokens: 5000,
	})
	tr.mu.Unlock()

	if avail := tr.Available(); avail != 25000 {
		t.Errorf("expected 25000 (only recent record counted), got %d", avail)
	}
}

func TestTokenRateTracker_WaitTime_FitsNow(t *testing.T) {
	tr := NewTokenRateTracker(30000)
	tr.Record(10000)
	wait := tr.WaitTime(15000)
	if wait != 0 {
		t.Errorf("expected 0 wait, got %s", wait)
	}
}

func TestTokenRateTracker_WaitTime_NeedsToWait(t *testing.T) {
	tr := NewTokenRateTracker(30000)

	// Record at 50 seconds ago — will expire in ~10 seconds.
	tr.mu.Lock()
	tr.window = append(tr.window, tokenRecord{
		at:     time.Now().Add(-50 * time.Second),
		tokens: 25000,
	})
	tr.mu.Unlock()

	wait := tr.WaitTime(10000)
	// Need 5000 more tokens freed. The 25K record expires in ~10s.
	if wait < 8*time.Second || wait > 12*time.Second {
		t.Errorf("expected wait around 10s, got %s", wait)
	}
}

func TestTokenRateTracker_WaitTime_ExceedsLimit(t *testing.T) {
	tr := NewTokenRateTracker(30000)

	// Record something recent.
	tr.mu.Lock()
	tr.window = append(tr.window, tokenRecord{
		at:     time.Now().Add(-5 * time.Second),
		tokens: 10000,
	})
	tr.mu.Unlock()

	// Request that exceeds the entire limit.
	wait := tr.WaitTime(50000)
	// Should return time until the full window clears (around 55s).
	if wait < 50*time.Second || wait > 60*time.Second {
		t.Errorf("expected wait around 55s for oversized request, got %s", wait)
	}
}

func TestTokenRateTracker_WaitTime_EmptyWindow(t *testing.T) {
	tr := NewTokenRateTracker(30000)
	wait := tr.WaitTime(20000)
	if wait != 0 {
		t.Errorf("expected 0 wait on empty window, got %s", wait)
	}
}

func TestTokenRateTracker_UpdateLimit(t *testing.T) {
	tr := NewTokenRateTracker(30000)
	tr.UpdateLimit(50000)
	if avail := tr.Available(); avail != 50000 {
		t.Errorf("expected 50000 after UpdateLimit, got %d", avail)
	}
}

func TestTokenRateTracker_UpdateLimit_IgnoresZero(t *testing.T) {
	tr := NewTokenRateTracker(30000)
	tr.UpdateLimit(0)
	if avail := tr.Available(); avail != 30000 {
		t.Errorf("expected 30000 (unchanged), got %d", avail)
	}
}

func TestTokenRateTracker_Remaining(t *testing.T) {
	tr := NewTokenRateTracker(30000)
	tr.Record(12000)
	avail, limit := tr.Remaining()
	if avail != 18000 {
		t.Errorf("expected 18000 available, got %d", avail)
	}
	if limit != 30000 {
		t.Errorf("expected 30000 limit, got %d", limit)
	}
}

func TestTokenRateTracker_ConcurrentAccess(t *testing.T) {
	tr := NewTokenRateTracker(100000)
	var wg sync.WaitGroup
	const goroutines = 50

	// Concurrent Record calls.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr.Record(100)
		}()
	}

	// Concurrent Available calls.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr.Available()
		}()
	}

	// Concurrent WaitTime calls.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr.WaitTime(500)
		}()
	}

	// Concurrent UpdateLimit calls.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr.UpdateLimit(100000)
		}()
	}

	wg.Wait()

	// Verify no panic occurred and state is consistent.
	avail := tr.Available()
	if avail < 0 || avail > 100000 {
		t.Errorf("available out of range after concurrent access: %d", avail)
	}
}
