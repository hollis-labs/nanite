package store

// Phase 2 item 02 (TASKS/phase-2/02-port-forward-dynamic-resolver.md).

import (
	"context"
	"errors"
	"testing"
)

func TestAgentContextResolverCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "context-resolver-crud")

	row := AgentContextResolver{
		AgentID:  agent.ID,
		SlotName: "weather",
		Kind:     "cmd",
		Run:      "echo hello",
		Timeout:  "5s",
		// Enabled is a plain bool, not a pointer -- the store layer
		// persists whatever the caller supplies (matching
		// AgentReflex.OptOutAllowed's established precedent); the
		// friendly "default true unless the API request says
		// otherwise" behavior lives at the API layer
		// (handleCreateAgentContextResolver), not here.
		Enabled: true,
	}
	id, err := s.InsertAgentContextResolver(ctx, row)
	if err != nil {
		t.Fatalf("InsertAgentContextResolver: %v", err)
	}
	if id == "" {
		t.Fatal("InsertAgentContextResolver did not return an id")
	}

	got, err := s.GetAgentContextResolver(ctx, id)
	if err != nil {
		t.Fatalf("GetAgentContextResolver: %v", err)
	}
	if got.AgentID != agent.ID || got.SlotName != "weather" || got.Kind != "cmd" || got.Run != "echo hello" {
		t.Errorf("GetAgentContextResolver mismatch: got %+v", got)
	}
	if !got.Enabled {
		t.Error("expected Enabled to round-trip as true")
	}
	if got.HeadersJSON != "{}" {
		t.Errorf("expected HeadersJSON default '{}', got %q", got.HeadersJSON)
	}
	if got.ResponseFormat != "text" {
		t.Errorf("expected ResponseFormat default 'text', got %q", got.ResponseFormat)
	}
	if got.CreatedAt == "" || got.UpdatedAt == "" {
		t.Error("InsertAgentContextResolver did not stamp timestamps")
	}

	list, err := s.ListAgentContextResolvers(ctx, agent.ID)
	if err != nil {
		t.Fatalf("ListAgentContextResolvers: %v", err)
	}
	if len(list) != 1 || list[0].ID != id {
		t.Fatalf("ListAgentContextResolvers: got %+v, want one row with id %q", list, id)
	}

	enabled, err := s.ListEnabledAgentContextResolvers(ctx, agent.ID)
	if err != nil {
		t.Fatalf("ListEnabledAgentContextResolvers: %v", err)
	}
	if len(enabled) != 1 {
		t.Fatalf("ListEnabledAgentContextResolvers: got %d rows, want 1", len(enabled))
	}

	got.Run = "echo updated"
	got.Enabled = false
	if err := s.UpdateAgentContextResolver(ctx, *got); err != nil {
		t.Fatalf("UpdateAgentContextResolver: %v", err)
	}
	updated, err := s.GetAgentContextResolver(ctx, id)
	if err != nil {
		t.Fatalf("GetAgentContextResolver after update: %v", err)
	}
	if updated.Run != "echo updated" || updated.Enabled {
		t.Errorf("UpdateAgentContextResolver did not persist: got %+v", updated)
	}

	// Disabling must remove the row from the boot-time enabled read path
	// without deleting it.
	enabledAfterDisable, err := s.ListEnabledAgentContextResolvers(ctx, agent.ID)
	if err != nil {
		t.Fatalf("ListEnabledAgentContextResolvers after disable: %v", err)
	}
	if len(enabledAfterDisable) != 0 {
		t.Fatalf("ListEnabledAgentContextResolvers after disable: got %+v, want none", enabledAfterDisable)
	}

	if err := s.DeleteAgentContextResolver(ctx, id); err != nil {
		t.Fatalf("DeleteAgentContextResolver: %v", err)
	}
	if _, err := s.GetAgentContextResolver(ctx, id); !errors.Is(err, ErrAgentContextResolverNotFound) {
		t.Fatalf("GetAgentContextResolver after delete: got err %v, want ErrAgentContextResolverNotFound", err)
	}
}

func TestAgentContextResolver_SlotNameUniquePerAgent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "context-resolver-unique-slot")

	first := AgentContextResolver{AgentID: agent.ID, SlotName: "weather", Kind: "cmd", Run: "echo one"}
	if _, err := s.InsertAgentContextResolver(ctx, first); err != nil {
		t.Fatalf("InsertAgentContextResolver first: %v", err)
	}
	second := AgentContextResolver{AgentID: agent.ID, SlotName: "weather", Kind: "cmd", Run: "echo two"}
	if _, err := s.InsertAgentContextResolver(ctx, second); err == nil {
		t.Fatal("expected InsertAgentContextResolver to reject a duplicate (agent_id, slot_name), got nil error")
	}
}

func TestAgentContextResolver_ValidationRejectsBadRows(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "context-resolver-validation")

	cases := []struct {
		name string
		row  AgentContextResolver
	}{
		{"missing kind", AgentContextResolver{AgentID: agent.ID, SlotName: "s1", Kind: "role_summary"}},
		{"cmd without run", AgentContextResolver{AgentID: agent.ID, SlotName: "s2", Kind: "cmd"}},
		{"http without url", AgentContextResolver{AgentID: agent.ID, SlotName: "s3", Kind: "http"}},
		{"http bad response_format", AgentContextResolver{AgentID: agent.ID, SlotName: "s4", Kind: "http", URL: "https://example.com", ResponseFormat: "xml"}},
		{"missing agent_id", AgentContextResolver{SlotName: "s5", Kind: "cmd", Run: "echo hi"}},
		{"missing slot_name", AgentContextResolver{AgentID: agent.ID, Kind: "cmd", Run: "echo hi"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.InsertAgentContextResolver(ctx, tc.row); err == nil {
				t.Fatalf("InsertAgentContextResolver(%+v): expected validation error, got nil", tc.row)
			}
		})
	}
}

// TestDeleteAgent_CascadesAgentContextResolvers mirrors
// TestDeleteAgent_CascadesAgentToolsAndDispatchAllowlist (agent_tools_test.go)
// for the new table's ON DELETE CASCADE FK.
func TestDeleteAgent_CascadesAgentContextResolvers(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "context-resolver-delete-cascade")

	if _, err := s.InsertAgentContextResolver(ctx, AgentContextResolver{
		AgentID: agent.ID, SlotName: "weather", Kind: "cmd", Run: "echo hi",
	}); err != nil {
		t.Fatalf("InsertAgentContextResolver: %v", err)
	}

	if err := s.DeleteAgent(context.Background(), agent.Slug); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}

	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM agent_context_resolvers WHERE agent_id = ?`, agent.ID).Scan(&n); err != nil {
		t.Fatalf("count agent_context_resolvers: %v", err)
	}
	if n != 0 {
		t.Fatalf("DeleteAgent left %d orphaned agent_context_resolvers rows", n)
	}
}
