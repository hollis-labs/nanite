package store

import (
	"context"
	"errors"
	"testing"
)

func TestAgentCycles_RoundTripAndComplete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := &AgentProfile{Name: "Cycle Agent", Slug: "cycle-agent", SystemPrompt: "x"}
	if err := s.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	id, err := s.InsertAgentCycle(ctx, AgentCycle{
		AgentID:          agent.ID,
		SessionID:        "sess-1",
		CycleKind:        AgentCycleKindWake,
		InputPointerJSON: `{"mail":"msg-1"}`,
	})
	if err != nil {
		t.Fatalf("InsertAgentCycle: %v", err)
	}
	got, err := s.GetAgentCycle(ctx, id)
	if err != nil {
		t.Fatalf("GetAgentCycle: %v", err)
	}
	if got.Status != AgentCycleStatusRunning {
		t.Fatalf("status = %q, want running", got.Status)
	}
	if got.DecisionsJSON != "[]" || got.OpenItemsJSON != "[]" {
		t.Fatalf("defaults not normalized: %+v", got)
	}

	if err := s.CompleteAgentCycle(ctx, AgentCycle{
		ID:                   id,
		Status:               AgentCycleStatusRebooted,
		OutputSummary:        "processed inbox and rebooted cleanly",
		DecisionsJSON:        `[{"id":"d1","summary":"use fresh cycle"}]`,
		OpenItemsJSON:        `[{"id":"o1","summary":"follow up"}]`,
		ArtifactPointersJSON: `["artifact://one"]`,
		ToolCacheRefsJSON:    `["tool://cache/ref"]`,
		RebootReason:         "request_boundary",
	}); err != nil {
		t.Fatalf("CompleteAgentCycle: %v", err)
	}
	got, err = s.GetAgentCycle(ctx, id)
	if err != nil {
		t.Fatalf("GetAgentCycle after complete: %v", err)
	}
	if got.Status != AgentCycleStatusRebooted {
		t.Errorf("status = %q, want rebooted", got.Status)
	}
	if got.EndedAt == "" {
		t.Errorf("EndedAt empty after complete")
	}
	if got.RebootReason != "request_boundary" {
		t.Errorf("RebootReason = %q", got.RebootReason)
	}

	list, err := s.ListAgentCycles(ctx, agent.ID, 10)
	if err != nil {
		t.Fatalf("ListAgentCycles: %v", err)
	}
	if len(list) != 1 || list[0].ID != id {
		t.Fatalf("ListAgentCycles = %+v, want one row %s", list, id)
	}
}

func TestCompleteAgentCycle_NotFound(t *testing.T) {
	s := newTestStore(t)
	err := s.CompleteAgentCycle(context.Background(), AgentCycle{ID: "missing"})
	if !errors.Is(err, ErrAgentCycleNotFound) {
		t.Fatalf("CompleteAgentCycle missing = %v, want ErrAgentCycleNotFound", err)
	}
}
