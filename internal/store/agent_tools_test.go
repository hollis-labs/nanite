package store

import (
	"context"
	"testing"
)

func TestGrantAgentTool_RoundTripAndIdempotent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "agent-tools-rt")

	toolID, err := s.UpsertKnownTool(ctx, "dev_read", "builtin", "available", "")
	if err != nil {
		t.Fatalf("UpsertKnownTool: %v", err)
	}

	if err := s.GrantAgentTool(ctx, agent.ID, toolID, "explicit"); err != nil {
		t.Fatalf("GrantAgentTool: %v", err)
	}
	// Re-granting the same pair is a no-op, not an error.
	if err := s.GrantAgentTool(ctx, agent.ID, toolID, "explicit"); err != nil {
		t.Fatalf("GrantAgentTool (repeat): %v", err)
	}

	names, err := s.ListAgentToolNames(ctx, agent.ID)
	if err != nil {
		t.Fatalf("ListAgentToolNames: %v", err)
	}
	if len(names) != 1 || names[0] != "dev_read" {
		t.Fatalf("ListAgentToolNames: got %v, want [dev_read]", names)
	}

	n, err := s.CountAgentTools(ctx, agent.ID)
	if err != nil {
		t.Fatalf("CountAgentTools: %v", err)
	}
	if n != 1 {
		t.Fatalf("CountAgentTools: got %d, want 1", n)
	}

	if err := s.RevokeAgentTool(ctx, agent.ID, toolID); err != nil {
		t.Fatalf("RevokeAgentTool: %v", err)
	}
	names2, err := s.ListAgentToolNames(ctx, agent.ID)
	if err != nil {
		t.Fatalf("ListAgentToolNames (after revoke): %v", err)
	}
	if len(names2) != 0 {
		t.Fatalf("ListAgentToolNames (after revoke): got %v, want []", names2)
	}
}

func TestGrantAgentTool_PreservesOriginalProvenance(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "agent-tools-provenance")

	toolID, err := s.UpsertKnownTool(ctx, "dev_read", "builtin", "available", "")
	if err != nil {
		t.Fatalf("UpsertKnownTool: %v", err)
	}

	if err := s.GrantAgentTool(ctx, agent.ID, toolID, "legacy_backfill"); err != nil {
		t.Fatalf("GrantAgentTool (legacy_backfill): %v", err)
	}
	// A later "explicit" grant attempt for the same pair must not overwrite
	// the original provenance tag (ON CONFLICT DO NOTHING).
	if err := s.GrantAgentTool(ctx, agent.ID, toolID, "explicit"); err != nil {
		t.Fatalf("GrantAgentTool (explicit, same pair): %v", err)
	}

	has, err := s.HasAgentToolGrantedVia(ctx, agent.ID, "legacy_backfill")
	if err != nil {
		t.Fatalf("HasAgentToolGrantedVia(legacy_backfill): %v", err)
	}
	if !has {
		t.Fatal("expected legacy_backfill provenance to survive a later explicit grant attempt")
	}
	hasExplicit, err := s.HasAgentToolGrantedVia(ctx, agent.ID, "explicit")
	if err != nil {
		t.Fatalf("HasAgentToolGrantedVia(explicit): %v", err)
	}
	if hasExplicit {
		t.Fatal("expected no explicit-provenance row -- the conflicting insert should have been a no-op")
	}
}

func TestAgentDispatchToolAllowlist_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "agent-dispatch-tool-allowlist-rt")

	toolID, err := s.UpsertKnownTool(ctx, "dev_bash", "builtin", "available", "")
	if err != nil {
		t.Fatalf("UpsertKnownTool: %v", err)
	}

	if err := s.GrantAgentDispatchTool(ctx, agent.ID, toolID); err != nil {
		t.Fatalf("GrantAgentDispatchTool: %v", err)
	}
	names, err := s.ListAgentDispatchToolNames(ctx, agent.ID)
	if err != nil {
		t.Fatalf("ListAgentDispatchToolNames: %v", err)
	}
	if len(names) != 1 || names[0] != "dev_bash" {
		t.Fatalf("ListAgentDispatchToolNames: got %v, want [dev_bash]", names)
	}

	if err := s.RevokeAgentDispatchTool(ctx, agent.ID, toolID); err != nil {
		t.Fatalf("RevokeAgentDispatchTool: %v", err)
	}
	names2, err := s.ListAgentDispatchToolNames(ctx, agent.ID)
	if err != nil {
		t.Fatalf("ListAgentDispatchToolNames (after revoke): %v", err)
	}
	if len(names2) != 0 {
		t.Fatalf("ListAgentDispatchToolNames (after revoke): got %v, want []", names2)
	}
}

// TestAgentTools_DoesNotTouchAgentKnownTools is the regression check this
// task's Done-means bullet asks for explicitly: granting agent_tools rows
// must never write into, or otherwise disturb, the separate
// agent_known_tools roster table.
func TestAgentTools_DoesNotTouchAgentKnownTools(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "agent-tools-vs-known-tools")

	toolID, err := s.UpsertKnownTool(ctx, "dev_read", "builtin", "available", "")
	if err != nil {
		t.Fatalf("UpsertKnownTool: %v", err)
	}
	if err := s.GrantAgentTool(ctx, agent.ID, toolID, "explicit"); err != nil {
		t.Fatalf("GrantAgentTool: %v", err)
	}

	known, err := s.ListAgentKnownTools(ctx, agent.ID)
	if err != nil {
		t.Fatalf("ListAgentKnownTools: %v", err)
	}
	if len(known) != 0 {
		t.Fatalf("GrantAgentTool leaked into agent_known_tools: %+v", known)
	}
}

func TestDeleteAgent_CascadesAgentToolsAndDispatchAllowlist(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "agent-tools-delete-cascade")

	toolID, err := s.UpsertKnownTool(ctx, "dev_read", "builtin", "available", "")
	if err != nil {
		t.Fatalf("UpsertKnownTool: %v", err)
	}
	if err := s.GrantAgentTool(ctx, agent.ID, toolID, "explicit"); err != nil {
		t.Fatalf("GrantAgentTool: %v", err)
	}
	if err := s.GrantAgentDispatchTool(ctx, agent.ID, toolID); err != nil {
		t.Fatalf("GrantAgentDispatchTool: %v", err)
	}

	if err := s.DeleteAgent(agent.Slug); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}

	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM agent_tools WHERE agent_id = ?`, agent.ID).Scan(&n); err != nil {
		t.Fatalf("count agent_tools: %v", err)
	}
	if n != 0 {
		t.Fatalf("DeleteAgent left %d orphaned agent_tools rows", n)
	}
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM agent_dispatch_tool_allowlist WHERE agent_id = ?`, agent.ID).Scan(&n); err != nil {
		t.Fatalf("count agent_dispatch_tool_allowlist: %v", err)
	}
	if n != 0 {
		t.Fatalf("DeleteAgent left %d orphaned agent_dispatch_tool_allowlist rows", n)
	}

	// The known_tools catalog row itself must survive -- it's global, not
	// scoped to this agent.
	if _, err := s.GetKnownToolByName(ctx, "dev_read"); err != nil {
		t.Fatalf("GetKnownToolByName(dev_read) after DeleteAgent: %v", err)
	}
}
