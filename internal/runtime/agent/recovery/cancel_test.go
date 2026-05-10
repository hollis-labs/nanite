package recovery

import (
	"context"
	"testing"
)

// TestCancel_ValidTokenAndSession exercises the happy path: a token
// registered for sessionID="s1" is cancellable via Cancel("s1", token).
// The bound CancelFunc fires; the entry is purged so a second call is
// a no-op (not a panic, not a double-cancel).
func TestCancel_ValidTokenAndSession(t *testing.T) {
	b := NewBroker(Dependencies{})
	_, cancel := context.WithCancel(context.Background())
	called := false
	wrappedCancel := func() {
		called = true
		cancel()
	}
	b.registerActiveRetry("s1", "tok-1", wrappedCancel)

	if !b.Cancel("s1", "tok-1") {
		t.Fatal("Cancel returned false; expected true for valid (session, token)")
	}
	if !called {
		t.Error("bound CancelFunc was not invoked")
	}
	if len(b.activeRetries) != 0 {
		t.Errorf("activeRetries should be empty after Cancel, got %d", len(b.activeRetries))
	}

	// Idempotent: a second Cancel for the same token is a no-op.
	if b.Cancel("s1", "tok-1") {
		t.Error("second Cancel returned true; expected false for already-cancelled token")
	}
}

// TestCancel_TokenScopedToOtherSession is the cross-session replay
// guard: a token issued for session A cannot be cancelled by a request
// claiming session B. The entry stays intact so the legitimate session
// can still cancel it.
func TestCancel_TokenScopedToOtherSession(t *testing.T) {
	b := NewBroker(Dependencies{})
	called := false
	b.registerActiveRetry("session-A", "tok-2", func() { called = true })

	if b.Cancel("session-B", "tok-2") {
		t.Error("Cancel returned true for cross-session replay; expected false")
	}
	if called {
		t.Error("bound CancelFunc fired despite session mismatch")
	}
	if len(b.activeRetries) != 1 {
		t.Errorf("activeRetries should still hold the entry, got %d", len(b.activeRetries))
	}

	// Legitimate session can still cancel.
	if !b.Cancel("session-A", "tok-2") {
		t.Error("Cancel returned false for legitimate (session, token) after rejected replay")
	}
}

// TestCancel_UnknownToken covers the FE-bug / stale-token path: the
// token isn't in the active map (already cancelled or never issued).
// Cancel returns false; no panic.
func TestCancel_UnknownToken(t *testing.T) {
	b := NewBroker(Dependencies{})
	if b.Cancel("s1", "never-existed") {
		t.Error("Cancel returned true for unknown token; expected false")
	}
}

// TestCancel_EmptyInputs short-circuits without registry lookups so a
// malformed FE request can't probe the active set.
func TestCancel_EmptyInputs(t *testing.T) {
	b := NewBroker(Dependencies{})
	b.registerActiveRetry("s1", "tok-3", func() {})

	if b.Cancel("", "tok-3") {
		t.Error("Cancel returned true for empty sessionID")
	}
	if b.Cancel("s1", "") {
		t.Error("Cancel returned true for empty token")
	}
	// Entry untouched.
	if len(b.activeRetries) != 1 {
		t.Errorf("activeRetries should still hold entry, got %d", len(b.activeRetries))
	}
}

// TestRegisterActiveRetry_BindsSessionID verifies the registry stores
// the sessionID alongside the cancel func — Cancel relies on this for
// validation. Empty token is silently dropped (the broker degrades to
// non-cancellable when newCancelToken's randomness fails).
func TestRegisterActiveRetry_BindsSessionID(t *testing.T) {
	b := NewBroker(Dependencies{})
	b.registerActiveRetry("s1", "tok-4", func() {})

	entry, ok := b.activeRetries["tok-4"]
	if !ok {
		t.Fatal("entry not registered")
	}
	if entry.sessionID != "s1" {
		t.Errorf("entry.sessionID = %q, want s1", entry.sessionID)
	}
	if entry.cancel == nil {
		t.Error("entry.cancel is nil")
	}

	// Empty token is dropped.
	b.registerActiveRetry("s2", "", func() {})
	if len(b.activeRetries) != 1 {
		t.Errorf("activeRetries grew to %d on empty token; expected 1", len(b.activeRetries))
	}
}
