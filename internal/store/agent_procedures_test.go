package store

import (
	"context"
	"errors"
	"testing"
)

func TestAgentProcedure_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "proc-rt")

	row := AgentProcedure{
		AgentID: agent.ID,
		Name:    "ship-checklist",
		Body:    "1. test\n2. build\n3. deploy",
		// Scope intentionally left empty to verify the "agent" default.
	}
	if err := s.InsertAgentProcedure(ctx, row); err != nil {
		t.Fatalf("InsertAgentProcedure: %v", err)
	}

	list, err := s.ListAgentProcedures(ctx, agent.ID)
	if err != nil {
		t.Fatalf("ListAgentProcedures: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListAgentProcedures: got %d, want 1", len(list))
	}

	got, err := s.GetAgentProcedure(ctx, agent.ID, "ship-checklist")
	if err != nil {
		t.Fatalf("GetAgentProcedure: %v", err)
	}
	if got.Body != "1. test\n2. build\n3. deploy" {
		t.Errorf("Body mismatch: got %q", got.Body)
	}
	if got.Scope != "agent" {
		t.Errorf("Scope: got %q, want %q (default)", got.Scope, "agent")
	}
	if got.CreatedAt == "" || got.UpdatedAt == "" {
		t.Errorf("timestamps empty: created %q updated %q", got.CreatedAt, got.UpdatedAt)
	}

	if err := s.DeleteAgentProcedure(ctx, agent.ID, "ship-checklist"); err != nil {
		t.Fatalf("DeleteAgentProcedure: %v", err)
	}
	if _, err := s.GetAgentProcedure(ctx, agent.ID, "ship-checklist"); !errors.Is(err, ErrAgentProcedureNotFound) {
		t.Fatalf("GetAgentProcedure after delete: got %v, want ErrAgentProcedureNotFound", err)
	}
}
