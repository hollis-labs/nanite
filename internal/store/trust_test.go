package store

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/hollis-labs/nanite/internal/dispatch"
)

// seedWorkspaceAndAgent inserts a minimal workspace and agent profile for
// trust resolver tests. Returns their IDs.
func seedWorkspaceAndAgent(t *testing.T, s *Store, agentKind string) (workspaceID, agentProfileID string) {
	t.Helper()
	workspaceID = "ws-" + uuid.New().String()
	if err := s.CreateWorkspace(&Workspace{ID: workspaceID, Name: "test-ws"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	ap := &AgentProfile{
		ID:           "ap-" + uuid.New().String(),
		Name:         "test-agent",
		Slug:         "test-agent-" + uuid.New().String()[:8],
		SystemPrompt: "test",
		Kind:         agentKind,
	}
	if err := s.CreateAgent(ap); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return workspaceID, ap.ID
}

func TestResolveTrust_Override(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	wsID, apID := seedWorkspaceAndAgent(t, s, "internal")

	// Insert an override → trusted.
	if err := s.PromoteRoleInWorkspace(ctx, wsID, apID, dispatch.TrustTrusted, "test"); err != nil {
		t.Fatalf("promote: %v", err)
	}

	tier, err := s.ResolveTrust(ctx, wsID, apID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tier != dispatch.TrustTrusted {
		t.Errorf("expected TrustTrusted, got %q", tier)
	}
}

func TestResolveTrust_FallbackToProfileDefault(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Internal agent has default_trust_tier = 'normal'.
	wsID, apID := seedWorkspaceAndAgent(t, s, "internal")

	// No override — should fall back to profile default.
	tier, err := s.ResolveTrust(ctx, wsID, apID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tier != dispatch.TrustNormal {
		t.Errorf("expected TrustNormal (profile default for internal), got %q", tier)
	}
}

func TestResolveTrust_ExternalDefaultUntrusted(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Migration 035 sets external/cli to 'untrusted'. Simulate by creating
	// an external agent and manually patching default_trust_tier (migration
	// handles this for real rows at boot).
	wsID, apID := seedWorkspaceAndAgent(t, s, "external")
	// The migration has already run (via store.New), so existing external rows
	// should have been repaired. But this agent was inserted after migration,
	// so we set it manually for the test.
	if _, err := s.DB.Exec(
		`UPDATE agent_profiles SET default_trust_tier = 'untrusted' WHERE id = ?`, apID,
	); err != nil {
		t.Fatalf("set default tier: %v", err)
	}

	tier, err := s.ResolveTrust(ctx, wsID, apID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tier != dispatch.TrustUntrusted {
		t.Errorf("expected TrustUntrusted for external agent, got %q", tier)
	}
}

func TestResolveTrust_MissingAgentDefaultsToNormal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	wsID, _ := seedWorkspaceAndAgent(t, s, "internal")

	// Non-existent agent profile ID.
	tier, err := s.ResolveTrust(ctx, wsID, "does-not-exist")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tier != dispatch.TrustNormal {
		t.Errorf("expected TrustNormal on miss, got %q", tier)
	}
}

func TestPromoteAndDemoteRoleInWorkspace(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	wsID, apID := seedWorkspaceAndAgent(t, s, "internal")

	// Promote to trusted.
	if err := s.PromoteRoleInWorkspace(ctx, wsID, apID, dispatch.TrustTrusted, "tester"); err != nil {
		t.Fatalf("promote: %v", err)
	}
	tier, err := s.ResolveTrust(ctx, wsID, apID)
	if err != nil {
		t.Fatalf("resolve after promote: %v", err)
	}
	if tier != dispatch.TrustTrusted {
		t.Errorf("expected TrustTrusted after promote, got %q", tier)
	}

	// Promote again to different tier (upsert).
	if err := s.PromoteRoleInWorkspace(ctx, wsID, apID, dispatch.TrustNormal, "tester"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	tier, err = s.ResolveTrust(ctx, wsID, apID)
	if err != nil {
		t.Fatalf("resolve after upsert: %v", err)
	}
	if tier != dispatch.TrustNormal {
		t.Errorf("expected TrustNormal after upsert, got %q", tier)
	}

	// Demote (remove override) — should fall back to profile default (normal for internal).
	if err := s.DemoteRoleInWorkspace(ctx, wsID, apID); err != nil {
		t.Fatalf("demote: %v", err)
	}
	tier, err = s.ResolveTrust(ctx, wsID, apID)
	if err != nil {
		t.Fatalf("resolve after demote: %v", err)
	}
	if tier != dispatch.TrustNormal {
		t.Errorf("expected TrustNormal after demote, got %q", tier)
	}
}

func TestListWorkspaceRoleTrust(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	wsID, apID := seedWorkspaceAndAgent(t, s, "internal")

	// Empty initially.
	rows, err := s.ListWorkspaceRoleTrust(ctx, wsID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}

	// Add one.
	if err := s.PromoteRoleInWorkspace(ctx, wsID, apID, dispatch.TrustTrusted, "test"); err != nil {
		t.Fatalf("promote: %v", err)
	}
	rows, err = s.ListWorkspaceRoleTrust(ctx, wsID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].TrustTier != string(dispatch.TrustTrusted) {
		t.Errorf("expected TrustTrusted, got %q", rows[0].TrustTier)
	}
}
