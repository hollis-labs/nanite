package subagent

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/messaging"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestSpawn_ReplyDelivery_ParentSlug_ToAgentIDResolution is the regression pin
// for CW-20260815-0027: when run.ParentAgentID contains a role slug (e.g.
// "operator") instead of a real agent_profiles.ID, reply delivery used to pass
// that slug as ToAgentID. While this doesn't cause auto-register collision
// (ToAgentID doesn't trigger auto-register), ValidateAgentID fails because the
// resolver can't find a row with ID=slug when the real row has a different ID
// (e.g. "agt-operator-001"). This caused reply delivery to fail silently,
// leaving the parent session without the subagent's result message.
//
// This test spawns a subagent with ParentAgentID set to a slug and verifies
// that:
//  1. The run completes successfully
//  2. The reply is delivered to the parent session (proves ToAgentID was
//     resolved correctly)
//  3. ValidateAgentID accepted the resolved ID
//
// Mirrors TestSpawn_ReplyDelivery_ExistingRoleSlug_NoAutoRegisterCollision but
// exercises the ToAgentID path instead of FromAgentID.
func TestSpawn_ReplyDelivery_ParentSlug_ToAgentIDResolution(t *testing.T) {
	db, st := newTestDB(t)

	// Create an "operator" profile where ID != slug. This mirrors the
	// real-world pattern where agent_profiles.ID is a generated UUID
	// or prefixed ID (e.g. "agt-operator-001") but slug is the bare
	// role name ("operator").
	if err := st.CreateAgent(&store.AgentProfile{
		ID:     "agt-operator-001",
		Slug:   "operator",
		Name:   "Operator",
		Kind:   "internal",
		Status: "active",
	}); err != nil {
		t.Fatalf("seed operator profile: %v", err)
	}

	// newTestDB already seeds the "worker" profile at id="blt-worker-001"
	// (migration 060) for the child role.

	msgStore := messaging.NewSQLiteStore(db)
	messagingSvc := messaging.NewService(msgStore, db, storeAgentResolver{st: st}, st)

	svc := NewService(db, EchoRunner{}, messagingSvc, nil, stubSettings{})
	svc.SetProfileResolver(st)

	// Spawn with ParentAgentID="operator" (the slug) instead of
	// "agt-operator-001" (the real ID). Before CW-20260815-0027 this
	// caused ValidateAgentID to fail on ToAgentID, silently dropping
	// the reply.
	id, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-parent",
		ParentAgentID:   "operator", // slug, not the real ID
		Role:            "worker",
		Prompt:          "do work",
		Mode:            ModeSync,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	run, err := svc.Status(context.Background(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if run.Status != StatusCompleted {
		t.Fatalf("Status = %q, want %q (Error=%q)", run.Status, StatusCompleted, run.Error)
	}

	// Assert that the reply was delivered successfully to the parent
	// session. Before the fix, ValidateAgentID would reject
	// ToAgentID="operator" (no row with ID=operator), causing
	// SendMessage to fail and the parent to never see the result.
	msgs, err := messagingSvc.RecentForSession(context.Background(), "sess-parent", 10)
	if err != nil {
		t.Fatalf("RecentForSession: %v", err)
	}

	replies := 0
	for _, m := range msgs {
		if m.Kind == messaging.KindSubagentResult {
			// Additional verification: the message should be TO the
			// resolved ID, not the slug.
			if m.ToAgentID != "agt-operator-001" {
				t.Errorf("reply ToAgentID = %q, want %q (should be resolved ID, not slug)",
					m.ToAgentID, "agt-operator-001")
			}
			replies++
		}
	}

	if replies != 1 {
		t.Fatalf("parent session has %d subagent_result message(s), want 1 — reply delivery failed (likely ToAgentID validation failed on slug)", replies)
	}

	// Verify the resolved ID logic: replyToAgentID should have returned
	// "agt-operator-001", not "operator".
	resolvedID := svc.replyToAgentID("operator")
	if resolvedID != "agt-operator-001" {
		t.Errorf("replyToAgentID(%q) = %q, want %q", "operator", resolvedID, "agt-operator-001")
	}

	// Also verify the fallback path: passing an already-valid ID should
	// return it unchanged.
	alreadyValidID := svc.replyToAgentID("agt-operator-001")
	if alreadyValidID != "agt-operator-001" {
		t.Errorf("replyToAgentID(%q) = %q, want %q (fallback path)", "agt-operator-001", alreadyValidID, "agt-operator-001")
	}
}
