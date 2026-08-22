package lifecycle

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/safego"
)

func TestGo_RunsAndShutdownDrains(t *testing.T) {
	m := NewManager("test")
	var ran atomic.Bool
	started := make(chan struct{})
	m.Go("work", func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		ran.Store(true)
	})
	<-started
	if got := m.Active(); got != 1 {
		t.Fatalf("active = %d, want 1", got)
	}
	if err := m.Shutdown(time.Second); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if !ran.Load() {
		t.Fatal("fn did not observe cancellation")
	}
	if got := m.Active(); got != 0 {
		t.Fatalf("active after shutdown = %d, want 0", got)
	}
}

// TestGoConcurrentWithShutdownNeverStartsAfterReturn is the admission-gate
// regression. Historically Go checked closed separately from wg.Add, allowing
// Shutdown to observe a zero WaitGroup and return in between those operations.
func TestGoConcurrentWithShutdownNeverStartsAfterReturn(t *testing.T) {
	for round := 0; round < 200; round++ {
		m := NewManager("admission-race")
		start := make(chan struct{})
		var callers sync.WaitGroup
		var shutdownReturned atomic.Bool
		var startedAfterReturn atomic.Bool

		for i := 0; i < 32; i++ {
			callers.Add(1)
			go func() {
				defer callers.Done()
				<-start
				m.Go("racer", func(ctx context.Context) {
					if shutdownReturned.Load() {
						startedAfterReturn.Store(true)
					}
					<-ctx.Done()
				})
			}()
		}

		shutdownDone := make(chan error, 1)
		go func() {
			<-start
			shutdownDone <- m.Shutdown(time.Second)
		}()
		close(start)
		if err := <-shutdownDone; err != nil {
			t.Fatalf("round %d shutdown: %v", round, err)
		}
		shutdownReturned.Store(true)
		callers.Wait()
		if startedAfterReturn.Load() {
			t.Fatalf("round %d admitted work started after Shutdown returned", round)
		}
	}
}

func TestShutdown_Timeout(t *testing.T) {
	m := NewManager("stuck")
	block := make(chan struct{})
	m.Go("blocker", func(ctx context.Context) {
		<-block // deliberately ignore ctx cancellation
	})
	// Give it a moment to register.
	time.Sleep(10 * time.Millisecond)

	err := m.Shutdown(20 * time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	var ste *ShutdownTimeoutError
	if !errors.As(err, &ste) {
		t.Fatalf("expected *ShutdownTimeoutError, got %T", err)
	}
	if ste.Label != "stuck" || ste.Remaining < 1 {
		t.Fatalf("bad ShutdownTimeoutError: %+v", ste)
	}
	// Release the stuck goroutine so the test doesn't leak.
	close(block)
}

func TestGo_AfterShutdownIsNoOp(t *testing.T) {
	m := NewManager("closed")
	if err := m.Shutdown(time.Second); err != nil {
		t.Fatal(err)
	}
	var ran atomic.Bool
	m.Go("late", func(ctx context.Context) { ran.Store(true) })
	time.Sleep(10 * time.Millisecond)
	if ran.Load() {
		t.Fatal("Go after Shutdown should not spawn")
	}
}

func TestGo_RecoversPanic(t *testing.T) {
	// Install a capturing panic hook so we can verify safego integration.
	var fired atomic.Int32
	prev := safego.SetPanicHook(func(label string, v any, stack []byte) {
		fired.Add(1)
	})
	defer safego.SetPanicHook(prev)

	m := NewManager("panics")
	m.Go("bad", func(ctx context.Context) { panic("boom") })

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if fired.Load() == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if fired.Load() != 1 {
		t.Fatal("panic hook did not fire")
	}
	// Manager should still shut down cleanly (the panicking goroutine
	// returned normally after recovery).
	if err := m.Shutdown(time.Second); err != nil {
		t.Fatalf("shutdown after panic: %v", err)
	}
}

func TestParentCancelPropagates(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	m := NewManagerWithContext(parent, "child")
	var seen atomic.Bool
	done := make(chan struct{})
	m.Go("watch", func(ctx context.Context) {
		<-ctx.Done()
		seen.Store(true)
		close(done)
	})
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("child ctx not cancelled by parent")
	}
	if !seen.Load() {
		t.Fatal("goroutine did not observe cancellation")
	}
	if err := m.Shutdown(time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestContextAccessor(t *testing.T) {
	m := NewManager("ctx")
	if m.Context() == nil {
		t.Fatal("nil context")
	}
	if err := m.Shutdown(time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case <-m.Context().Done():
	default:
		t.Fatal("context not cancelled after shutdown")
	}
}

// TestShutdown_InvokesCancel is the G118 regression: asserts that Shutdown
// invokes the CancelFunc returned by context.WithCancel inside
// NewManagerWithContext. The CancelFunc is stored on the Manager and the
// ownership contract is that Shutdown MUST invoke it; any refactor that
// drops the invocation would leak cancel nodes off the parent context.
func TestShutdown_InvokesCancel(t *testing.T) {
	parent := context.Background()
	m := NewManagerWithContext(parent, "cancel-invoked")

	// Before shutdown, context is live.
	select {
	case <-m.Context().Done():
		t.Fatal("manager context cancelled before Shutdown")
	default:
	}

	if err := m.Shutdown(time.Second); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	// After shutdown, the context must be done with Canceled (not
	// DeadlineExceeded from a parent timer).
	select {
	case <-m.Context().Done():
	default:
		t.Fatal("manager context not cancelled after Shutdown")
	}
	if err := m.Context().Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("ctx err after Shutdown = %v, want context.Canceled", err)
	}
}

// TestShutdown_RepeatedCyclesNoLeak asserts that creating and draining many
// Managers back-to-back does not leak cancel nodes off the parent context.
// Combined with the package-level goleak TestMain, this is the functional
// regression for G118.
func TestShutdown_RepeatedCyclesNoLeak(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()

	for i := 0; i < 50; i++ {
		m := NewManagerWithContext(parent, "cycle")
		var ran atomic.Bool
		m.Go("w", func(ctx context.Context) {
			<-ctx.Done()
			ran.Store(true)
		})
		if err := m.Shutdown(time.Second); err != nil {
			t.Fatalf("cycle %d shutdown: %v", i, err)
		}
		if !ran.Load() {
			t.Fatalf("cycle %d: goroutine did not observe cancel", i)
		}
		// Second Shutdown is idempotent and must not panic or block.
		if err := m.Shutdown(10 * time.Millisecond); err != nil {
			t.Fatalf("cycle %d second shutdown: %v", i, err)
		}
	}
}
