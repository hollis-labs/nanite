package store

import (
	"context"
	"errors"
	"testing"
)

func TestAgentKnownSkill_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "known-skill-rt")

	row := AgentKnownSkill{
		AgentID:   agent.ID,
		SkillName: "code-review",
		Pinned:    false,
		Reason:    "boot-default",
	}
	if err := s.InsertAgentKnownSkill(ctx, row); err != nil {
		t.Fatalf("InsertAgentKnownSkill: %v", err)
	}

	list, err := s.ListAgentKnownSkills(ctx, agent.ID)
	if err != nil {
		t.Fatalf("ListAgentKnownSkills: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListAgentKnownSkills: got %d, want 1", len(list))
	}

	got, err := s.GetAgentKnownSkill(ctx, agent.ID, "code-review")
	if err != nil {
		t.Fatalf("GetAgentKnownSkill: %v", err)
	}
	if got.SkillName != "code-review" {
		t.Errorf("SkillName: got %q, want %q", got.SkillName, "code-review")
	}
	if got.Pinned {
		t.Error("Pinned: got true, want false")
	}
	if got.Reason != "boot-default" {
		t.Errorf("Reason: got %q, want %q", got.Reason, "boot-default")
	}
	if got.AddedAt == "" {
		t.Error("AddedAt: expected default-populated timestamp, got empty")
	}

	if err := s.DeleteAgentKnownSkill(ctx, agent.ID, "code-review"); err != nil {
		t.Fatalf("DeleteAgentKnownSkill: %v", err)
	}
	if _, err := s.GetAgentKnownSkill(ctx, agent.ID, "code-review"); !errors.Is(err, ErrAgentKnownSkillNotFound) {
		t.Fatalf("GetAgentKnownSkill after delete: got %v, want ErrAgentKnownSkillNotFound", err)
	}
}
