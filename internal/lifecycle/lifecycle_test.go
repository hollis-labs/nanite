package lifecycle

import (
	"context"
	"errors"
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
