// Package lifecycle provides a tracked-goroutine Manager with deterministic
// shutdown semantics.
//
// A Manager owns a root context that is cancelled on Shutdown, a WaitGroup
// tracking every goroutine it has spawned, and an OTel tracer emitting span
// events at shutdown milestones (Start / WaitingOn / ForceExit).
//
// Typical use is composition: a subsystem (internal/worker.Manager, etc.)
// embeds a *lifecycle.Manager and delegates its goroutine spawning and
// shutdown behavior to it.
//
// All goroutines spawned via Manager.Go are wrapped by internal/safego, so
// panics do not propagate to the caller.
package lifecycle

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/hollis-labs/nanite/internal/safego"
)

var tracer = otel.Tracer("nanite/lifecycle")

// Manager coordinates tracked goroutines and shutdown.
type Manager struct {
	label string

	ctx    context.Context
	cancel context.CancelFunc

	wg sync.WaitGroup

	// active counts currently-running goroutines (for diagnostics).
	active atomic.Int64

	closed atomic.Bool
}

// NewManager returns a new Manager rooted at the background context.
func NewManager(label string) *Manager {
	return NewManagerWithContext(context.Background(), label)
}

// NewManagerWithContext returns a Manager whose root context is derived from
// parent. Cancelling parent also cancels the manager.
//
// Ownership contract: the returned Manager owns the CancelFunc. It is stored
// on the struct and invoked unconditionally by Shutdown (including the
// idempotent re-entry path). Callers that construct a Manager MUST call
// Shutdown — treat it as a `defer mgr.Shutdown(...)` sibling to `defer
// Close()`. Parent-context cancellation alone will cancel the manager's
// context but does not release the CancelFunc allocation; Shutdown does.
func NewManagerWithContext(parent context.Context, label string) *Manager {
	ctx, cancel := context.WithCancel(parent) //nolint:gosec // G118: cancel is stored on Manager and invoked by (*Manager).Shutdown; see ownership contract above. Verified by TestShutdown_InvokesCancel.
	m := &Manager{label: label, ctx: ctx, cancel: cancel}
	return m
}

// Context returns the manager's root context. It is cancelled on Shutdown.
func (m *Manager) Context() context.Context { return m.ctx }

// Active returns the current count of tracked goroutines still running.
func (m *Manager) Active() int64 { return m.active.Load() }

// Go spawns fn as a tracked, panic-safe goroutine. fn receives the manager's
// context, which is cancelled on Shutdown. The returned goroutine is counted
// in the Manager's WaitGroup; Shutdown will wait on it.
//
// If the manager is already closed, Go returns without spawning.
func (m *Manager) Go(label string, fn func(ctx context.Context)) {
	if m.closed.Load() {
		return
	}
	m.wg.Add(1)
	m.active.Add(1)
	safego.Go(m.ctx, m.label+"."+label, func() {
		defer m.wg.Done()
		defer m.active.Add(-1)
		fn(m.ctx)
	})
}

// Shutdown cancels the manager's context and waits up to maxWait for all
// tracked goroutines to exit. It returns context.DeadlineExceeded if any
// goroutine is still running after maxWait.
//
// Shutdown is idempotent; the second call waits on the same WaitGroup and
// returns immediately once all goroutines have completed.
func (m *Manager) Shutdown(maxWait time.Duration) error {
	m.closed.Store(true)

	ctx, span := tracer.Start(m.ctx, "lifecycle.shutdown",
		trace.WithAttributes(
			attribute.String("label", m.label),
			attribute.Int64("active", m.active.Load()),
			attribute.Float64("max_wait_seconds", maxWait.Seconds()),
		))
	defer span.End()
	span.AddEvent("shutdown.start")

	// Cancel after recording the start event so the event reflects the
	// pre-cancel active count.
	m.cancel()

	done := make(chan struct{})
	safego.Go(ctx, m.label+".shutdown-wait", func() {
		m.wg.Wait()
		close(done)
	})

	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	select {
	case <-done:
		span.AddEvent("shutdown.drained",
			trace.WithAttributes(attribute.Int64("remaining", 0)))
		return nil
	case <-timer.C:
		span.AddEvent("shutdown.waiting-on",
			trace.WithAttributes(attribute.Int64("remaining", m.active.Load())))
	}

	// Grace tick: give one more short window to drain; this matches how
	// production systems tend to log a "still waiting" notice. We still
	// return DeadlineExceeded, but the span event records the state.
	graceDone := make(chan struct{})
	safego.Go(ctx, m.label+".shutdown-grace", func() {
		m.wg.Wait()
		close(graceDone)
	})
	select {
	case <-graceDone:
		return nil
	case <-time.After(10 * time.Millisecond):
	}
	span.AddEvent("shutdown.force-exit",
		trace.WithAttributes(attribute.Int64("remaining", m.active.Load())))
	return errors.Join(context.DeadlineExceeded,
		&ShutdownTimeoutError{Label: m.label, Remaining: m.active.Load()})
}

// ShutdownTimeoutError records the number of goroutines still running at
// timeout. Callers can errors.As to extract it.
type ShutdownTimeoutError struct {
	Label     string
	Remaining int64
}

func (e *ShutdownTimeoutError) Error() string {
	return "lifecycle: shutdown timeout for " + e.Label
}
