package store

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/hollis-labs/nanite/internal/dispatch"
)

// seedAgent creates a minimal agent profile for trust resolver tests.
// Returns its ID.
//
// Phase 0 item 20 (retire workspaces): this used to also seed a workspace
// row for workspace_role_trust override tests. workspace_role_trust is
// retired in full (operator-confirmed 2026-08-18) — ResolveTrust no longer
// takes a workspaceID, so there's nothing left to seed but the agent.
func seedAgent(t *testing.T, s *Store, agentKind string) (agentProfileID string) {
	t.Helper()
	ap := &AgentProfile{
		ID:           "ap-" + uuid.New().String(),
		Name:         "test-agent",
		Slug:         "test-agent-" + uuid.New().String()[:8],
		SystemPrompt: "test",
		Kind:         agentKind,
	}
	if err := s.CreateAgent(context.Background(), ap); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return ap.ID
}

func TestResolveTrust_FallbackToProfileDefault(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Internal agent has default_trust_tier = 'normal'.
	apID := seedAgent(t, s, "internal")

	tier, err := s.ResolveTrust(ctx, apID)
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
	apID := seedAgent(t, s, "external")
	// The migration has already run (via store.New), so existing external rows
	// should have been repaired. But this agent was inserted after migration,
	// so we set it manually for the test.
	if _, err := s.DB.Exec(
		`UPDATE agent_profiles SET default_trust_tier = 'untrusted' WHERE id = ?`, apID,
	); err != nil {
		t.Fatalf("set default tier: %v", err)
	}

	tier, err := s.ResolveTrust(ctx, apID)
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

	// Non-existent agent profile ID.
	tier, err := s.ResolveTrust(ctx, "does-not-exist")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tier != dispatch.TrustNormal {
		t.Errorf("expected TrustNormal on miss, got %q", tier)
	}
}
