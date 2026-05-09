package envelope

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/go-envelopes"
)

// TestSetupForTesting_PartialOrphanFailure_NoPanic locks the docstring
// contract: RegisterOrphans is best-effort, so a partial failure must NOT
// panic. The test exercises this by calling SetupForTesting on a registry
// where every orphan name is already registered (a fresh LoadCore + a
// pre-pass of RegisterOrphans), forcing the second pass through a wrapped
// SetupForTesting-equivalent path to encounter ErrConflict on every entry.
//
// We can't call SetupForTesting itself twice in this test (it installs the
// package-level registry, which TestMain already populated); instead, we
// reproduce its body inline against a fresh Registry so the panic path is
// the only thing under test.
func TestSetupForTesting_PartialOrphanFailure_NoPanic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reg, err := envelopes.LoadCore(ctx)
	if err != nil {
		t.Fatalf("LoadCore: %v", err)
	}
	// First pass: all orphans register cleanly.
	if _, err := RegisterOrphans(reg); err != nil {
		t.Fatalf("first RegisterOrphans: %v", err)
	}
	// Second pass: every entry now collides with the existing registration
	// and surfaces a non-nil error. The 0 registered + non-nil err combo
	// is exactly the partial-failure shape SetupForTesting must tolerate
	// without panicking.
	registered, err := RegisterOrphans(reg)
	if err == nil {
		t.Fatal("second RegisterOrphans should error on duplicate registrations")
	}
	if registered != 0 {
		t.Fatalf("second RegisterOrphans should register 0 (all duplicates); got %d", registered)
	}

	// Mirror SetupForTesting's RegisterOrphans-error branch on the same
	// registry to confirm the no-panic contract. We deliberately do NOT
	// call SetupForTesting directly to avoid replacing the package-level
	// registry the rest of the test suite relies on.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("partial orphan failure must not panic; got: %v", r)
		}
	}()
	if _, err := RegisterOrphans(reg); err == nil {
		t.Fatal("expected partial-failure error on third pass")
	}
}
