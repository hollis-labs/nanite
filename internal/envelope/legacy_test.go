package envelope

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/go-envelopes"
)

// TestRegisterOrphans_EmptyListIsNoop locks the current state of
// OrphanTypes: all five original entries (giphy-modal, kb-result,
// resolution-capture, ticket-form, ticket-confirmation) were removed by
// the Phase 0 plugin cuts (15a-cut-giphy, 15c-cut-support-ticket), leaving
// OrphanTypes empty. RegisterOrphans is therefore a guaranteed no-op today
// — nothing to register, nothing to collide on, no error possible on
// repeated calls. This replaces the former
// TestSetupForTesting_PartialOrphanFailure_NoPanic, which exercised
// RegisterOrphans' best-effort partial-failure/no-panic contract via a
// real duplicate-registration collision — that scenario needs at least
// one OrphanTypes entry to reproduce, so it can't be exercised against
// the real package-level list until a future orphan schema is added
// there. If that happens, restore a duplicate-registration variant of
// this test alongside it.
func TestRegisterOrphans_EmptyListIsNoop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reg, err := envelopes.LoadCore(ctx)
	if err != nil {
		t.Fatalf("LoadCore: %v", err)
	}
	for i := range 3 {
		registered, err := RegisterOrphans(reg)
		if err != nil {
			t.Fatalf("pass %d: RegisterOrphans returned an error against an empty OrphanTypes list: %v", i, err)
		}
		if registered != 0 {
			t.Fatalf("pass %d: RegisterOrphans registered %d types; want 0 (OrphanTypes is empty)", i, registered)
		}
	}
}
