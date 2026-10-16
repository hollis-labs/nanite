package store

import (
	"context"

	"github.com/hollis-labs/nanite/internal/dispatch"
)

// ResolveTrust implements dispatch.TrustResolver.
//
// Phase 0 item 20 (TASKS/phase-0/20-retire-workspaces-and-instance-mechanism.md,
// operator-confirmed 2026-08-18): the workspace_role_trust override table
// (and the in-app `workspaces` table it was scoped to) is retired in full,
// not collapsed to a global override. Trust resolution reverts to
// unconditional base-tier resolution:
//
//  1. agent_profiles.default_trust_tier for agentProfileID
//  2. TrustNormal when that lookup misses (safe default)
func (s *Store) ResolveTrust(ctx context.Context, agentProfileID string) (dispatch.TrustTier, error) {
	// Intrinsic content, old profile columns and claimed IDs cannot confer
	// execution trust. Until the verified host actor port is adopted, refuse.
	return dispatch.TrustUntrusted, ErrVerifiedActorRequired
}
