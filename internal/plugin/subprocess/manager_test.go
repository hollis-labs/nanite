package subprocess

import (
	"context"
	"testing"
	"time"
)

// TestManagerStopAfterCleanExit is the regression for BLG-20260414-005.
//
// Previously: waitCh was a buffered(1) channel read by both waitForExit and
// Stop. On a clean exit (process terminates before Stop is called), waitForExit
// drained the single buffered value; Stop's subsequent <-waitCh blocked
// forever, even past the force-kill branch, because the kill path also read
// from the now-empty channel.
//
// After the fix: cmdWait closes waitCh after the single write, so late readers
// unblock immediately. Stop must return promptly when the process has already
// exited, with or without waitForExit having observed the error.
func TestManagerStopAfterCleanExit(t *testing.T) {
	// /bin/sh -c "exit 0" — spawns, exits immediately, no RPC surface.
	mgr := NewManager(ManagerConfig{
		Command:         "/bin/sh",
		Args:            []string{"-c", "exit 0"},
		HealthInterval:  0, // disable
		ShutdownTimeout: 500 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := mgr.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Let the process exit and waitForExit drain waitCh.
	time.Sleep(100 * time.Millisecond)

	// Stop should return promptly. Pre-fix this blocked forever.
	done := make(chan error, 1)
	go func() { done <- mgr.Stop() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not return within 3s — deadlock regression")
	}
}

// TestManagerStopWhileRunning verifies the happy path: subprocess is alive,
// Stop triggers unload attempt (will fail since /bin/cat ignores JSON-RPC)
// then force-kills after ShutdownTimeout. Must still return bounded.
func TestManagerStopWhileRunning(t *testing.T) {
	mgr := NewManager(ManagerConfig{
		Command:         "/bin/cat", // stays alive reading stdin
		HealthInterval:  0,
		ShutdownTimeout: 300 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := mgr.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- mgr.Stop() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not return within 3s")
	}
}
