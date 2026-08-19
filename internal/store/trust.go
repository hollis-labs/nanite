package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/hollis-labs/nanite/internal/dispatch"
)

// WorkspaceRoleTrust is a single row from workspace_role_trust.
type WorkspaceRoleTrust struct {
	WorkspaceID    string `json:"workspace_id"`
	AgentProfileID string `json:"agent_profile_id"`
	TrustTier      string `json:"trust_tier"`
	PromotedAt     string `json:"promoted_at"`
	PromotedBy     string `json:"promoted_by"`
}

// ResolveTrust implements dispatch.TrustResolver.
//
// Lookup order:
//  1. workspace_role_trust override for (workspaceID, agentProfileID)
//  2. agent_profiles.default_trust_tier for agentProfileID
//  3. TrustNormal when both queries miss (safe default)
func (s *Store) ResolveTrust(ctx context.Context, workspaceID, agentProfileID string) (dispatch.TrustTier, error) {
	// 1. Workspace-scoped override.
	var tier string
	err := s.DB.QueryRowContext(ctx,
		`SELECT trust_tier FROM workspace_role_trust
		 WHERE workspace_id = ? AND agent_profile_id = ?`,
		workspaceID, agentProfileID,
	).Scan(&tier)
	if err == nil {
		t := dispatch.TrustTier(tier)
		if !t.IsValid() {
			return dispatch.TrustNormal, fmt.Errorf("store: invalid trust_tier %q in workspace_role_trust", tier)
		}
		return t, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return dispatch.TrustNormal, fmt.Errorf("store: resolve trust override: %w", err)
	}

	// 2. Agent profile default.
	err = s.DB.QueryRowContext(ctx,
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

	// 3. Safe default — unknown agent ID means treat as normal (require approval).
	return dispatch.TrustNormal, nil
}

// PromoteRoleInWorkspace upserts a trust tier for (workspaceID, agentProfileID).
// promotedBy is a free-form attribution string (user ID, migration name, etc.).
func (s *Store) PromoteRoleInWorkspace(ctx context.Context, workspaceID, agentProfileID string, tier dispatch.TrustTier, promotedBy string) error {
	if !tier.IsValid() {
		return fmt.Errorf("store: invalid trust tier %q", tier)
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO workspace_role_trust (workspace_id, agent_profile_id, trust_tier, promoted_by, promoted_at)
		 VALUES (?, ?, ?, ?, datetime('now'))
		 ON CONFLICT(workspace_id, agent_profile_id)
		 DO UPDATE SET trust_tier = excluded.trust_tier,
		               promoted_by = excluded.promoted_by,
		               promoted_at = excluded.promoted_at`,
		workspaceID, agentProfileID, string(tier), promotedBy,
	)
	if err != nil {
		return fmt.Errorf("store: promote role trust: %w", err)
	}
	return nil
}

// DemoteRoleInWorkspace removes the workspace-scoped trust override for
// (workspaceID, agentProfileID), causing ResolveTrust to fall back to
// agent_profiles.default_trust_tier.
func (s *Store) DemoteRoleInWorkspace(ctx context.Context, workspaceID, agentProfileID string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM workspace_role_trust WHERE workspace_id = ? AND agent_profile_id = ?`,
		workspaceID, agentProfileID,
	)
	if err != nil {
		return fmt.Errorf("store: demote role trust: %w", err)
	}
	return nil
}

// ListWorkspaceRoleTrust returns all trust overrides for a workspace.
func (s *Store) ListWorkspaceRoleTrust(ctx context.Context, workspaceID string) ([]WorkspaceRoleTrust, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT workspace_id, agent_profile_id, trust_tier,
		        COALESCE(promoted_at,''), COALESCE(promoted_by,'')
		 FROM workspace_role_trust
		 WHERE workspace_id = ?
		 ORDER BY agent_profile_id`,
		workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list workspace role trust: %w", err)
	}
	defer rows.Close()

	out := make([]WorkspaceRoleTrust, 0)
	for rows.Next() {
		var r WorkspaceRoleTrust
		if err := rows.Scan(&r.WorkspaceID, &r.AgentProfileID, &r.TrustTier, &r.PromotedAt, &r.PromotedBy); err != nil {
			return nil, fmt.Errorf("store: scan workspace role trust: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
