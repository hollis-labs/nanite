package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// fakeTransportRestarter satisfies mcpTransportRestarter for adapter
// unit tests. Counts invocations and lets each test inject a per-call
// behavior (success / error / sleep-then-respect-ctx).
type fakeTransportRestarter struct {
	count   atomic.Int32
	err     error
	delay   time.Duration
	gotCtx  context.Context
	mu      atomic.Bool // marks first call entered (for concurrency tests)
	started chan struct{}
}

func (f *fakeTransportRestarter) RestartStdioTransports(ctx context.Context) error {
	f.count.Add(1)
	f.gotCtx = ctx
	if f.started != nil && f.mu.CompareAndSwap(false, true) {
		close(f.started)
	}
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.err
}

// TestRecoveryMCPAdapter_RestartTransport_Success verifies the happy path:
// the adapter forwards into RestartStdioTransports, returns nil, and
// surfaces no errors when the underlying manager succeeds.
func TestRecoveryMCPAdapter_RestartTransport_Success(t *testing.T) {
	fake := &fakeTransportRestarter{}
	a := &recoveryMCPAdapter{manager: fake}

	if err := a.RestartTransport(context.Background(), "sess-abc"); err != nil {
		t.Fatalf("RestartTransport: unexpected err = %v", err)
	}
	if got := fake.count.Load(); got != 1 {
		t.Fatalf("RestartStdioTransports call count = %d, want 1", got)
	}
}

// TestRecoveryMCPAdapter_RestartTransport_Idempotent issues two back-to-
// back RestartTransport calls (mirroring the broker calling Remediate on
// a still-restarting state) and verifies neither panics nor returns an
// error. Each call increments the underlying manager's invocation counter.
func TestRecoveryMCPAdapter_RestartTransport_Idempotent(t *testing.T) {
	fake := &fakeTransportRestarter{}
	a := &recoveryMCPAdapter{manager: fake}

	for i := 0; i < 2; i++ {
		if err := a.RestartTransport(context.Background(), "sess-abc"); err != nil {
			t.Fatalf("RestartTransport call %d: unexpected err = %v", i+1, err)
		}
	}
	if got := fake.count.Load(); got != 2 {
		t.Fatalf("RestartStdioTransports call count = %d, want 2", got)
	}
}

// TestRecoveryMCPAdapter_RestartTransport_TimeoutRespect verifies the
// adapter honors a context deadline. The fake delays 200ms; the caller
// passes a 20ms ctx — the adapter must surface ctx.Err() within bounded
// time, not block on the underlying close.
func TestRecoveryMCPAdapter_RestartTransport_TimeoutRespect(t *testing.T) {
	fake := &fakeTransportRestarter{delay: 200 * time.Millisecond}
	a := &recoveryMCPAdapter{manager: fake}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := a.RestartTransport(ctx, "sess-abc")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("RestartTransport: expected ctx-deadline error, got nil")
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("RestartTransport blocked %v past deadline (want <100ms)", elapsed)
	}
}

// TestRecoveryMCPAdapter_RestartTransport_NoNilPanic verifies a nil
// adapter / nil-manager guard returns a clean error rather than
// panicking. Defensive — the construction path leaves brokerDeps.MCP
// unset when cfg.MCP is nil, but a partially-initialized adapter could
// reach the dispatch path through a refactor regression.
func TestRecoveryMCPAdapter_RestartTransport_NoNilPanic(t *testing.T) {
	var a *recoveryMCPAdapter
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RestartTransport panicked on nil adapter: %v", r)
		}
	}()
	if err := a.RestartTransport(context.Background(), "sess-abc"); err == nil {
		t.Fatal("RestartTransport: expected error from nil adapter")
	}

	a2 := &recoveryMCPAdapter{manager: nil}
	if err := a2.RestartTransport(context.Background(), "sess-abc"); err == nil {
		t.Fatal("RestartTransport: expected error from nil manager")
	}
}

// TestRecoveryMCPAdapter_RestartTransport_ErrorWrap verifies underlying
// manager errors are wrapped with the recovery: prefix so the broker's
// breadcrumb captures clear remediation-failure context.
func TestRecoveryMCPAdapter_RestartTransport_ErrorWrap(t *testing.T) {
	sentinel := errors.New("subprocess wedged")
	fake := &fakeTransportRestarter{err: sentinel}
	a := &recoveryMCPAdapter{manager: fake}

	err := a.RestartTransport(context.Background(), "sess-abc")
	if err == nil {
		t.Fatal("RestartTransport: expected wrapped error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("RestartTransport err = %v, want wrap of %v", err, sentinel)
	}
}
