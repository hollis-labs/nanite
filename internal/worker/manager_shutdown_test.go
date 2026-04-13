package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/lifecycle"
)

// blockingDelegator blocks until its context is cancelled, then returns the
// context error. It models a long-running provider call that must honour
// cancellation for Shutdown to complete in bounded time.
type blockingDelegator struct {
	started chan struct{}
}

func (b *blockingDelegator) DelegateTask(ctx context.Context, req chat.DelegationRequest) (*chat.DelegationResult, error) {
	select {
	case <-b.started:
	default:
		close(b.started)
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestShutdown_CancelsInFlightWorkAndDrains exercises the lifecycle-based
// Shutdown. A SpawnFull call is in flight when Shutdown(500ms) is invoked;
// Shutdown must cancel the worker's context, drain tracked goroutines
// (heartbeat + retention timer), and return within the budget — rather than
// blocking for the 30-second retention sleep that previously leaked past
// process exit (BLG-001).
//
// The package-level TestMain calls goleak.VerifyTestMain, so any leaked
// goroutines from this test cause the package to fail.
func TestShutdown_CancelsInFlightWorkAndDrains(t *testing.T) {
	deleg := &blockingDelegator{started: make(chan struct{})}
	mgr := newTestManager(deleg)

	spawnDone := make(chan struct{})
	go func() {
		_, _ = mgr.SpawnFull(context.Background(), SpawnRequest{
			ParentSessionID: "parent-shutdown",
			Title:           "blocker",
			AgentID:         "agent-shutdown",
		})
		close(spawnDone)
	}()

	// Wait for the delegator to actually start blocking — otherwise Shutdown
	// might race the goroutine and not exercise cancellation.
	select {
	case <-deleg.started:
	case <-time.After(2 * time.Second):
		t.Fatal("delegator did not start within 2s")
	}

	start := time.Now()
	err := mgr.Shutdown(500 * time.Millisecond)
	elapsed := time.Since(start)

	// Shutdown must return within the budget plus a small slack; the old
	// code would block behind the 30-second retention sleep.
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("Shutdown took %s, want < 1.5s", elapsed)
	}

	// It is acceptable either way: either all goroutines drained (nil) or
	// the heartbeat/retention goroutine was still exiting (DeadlineExceeded
	// wrapped with ShutdownTimeoutError). Both prove Shutdown respects the
	// timeout rather than blocking indefinitely.
	if err != nil {
		var ste *lifecycle.ShutdownTimeoutError
		if !errors.As(err, &ste) {
			t.Fatalf("unexpected Shutdown error type: %v", err)
		}
	}

	// SpawnFull should also return quickly now that the delegator context
	// is cancelled.
	select {
	case <-spawnDone:
	case <-time.After(2 * time.Second):
		t.Fatal("SpawnFull did not return after Shutdown")
	}
}
