package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

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
	var tier string
	err := s.DB.QueryRowContext(ctx,
		`SELECT default_trust_tier FROM agent_profiles WHERE id = ?`,
		agentProfileID,
	).Scan(&tier)
	if err == nil {
		t := dispatch.TrustTier(tier)
		if !t.IsValid() {
			return dispatch.TrustNormal, fmt.Errorf("store: invalid default_trust_tier %q for agent %s", tier, agentProfileID)
		}
		return t, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return dispatch.TrustNormal, fmt.Errorf("store: resolve agent default trust: %w", err)
	}

	// Safe default — unknown agent ID means treat as normal (require approval).
	return dispatch.TrustNormal, nil
}
