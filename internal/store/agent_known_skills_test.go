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

// TestAgentKnownSkill_IsBareAssignment is a regression guard for
// TASKS/skills/02's fix-required section (2026-08-21 review): a row created
// only by AssignSkillToAgent's bare INSERT (agent_id, skill_name — no
// known-skill data of its own) must read back as bare, and a row carrying
// any real known-skill/grant data must not.
func TestAgentKnownSkill_IsBareAssignment(t *testing.T) {
	bare := AgentKnownSkill{AgentID: "a1", SkillName: "s1"}
	if !bare.IsBareAssignment() {
		t.Errorf("zero-value row (beyond agent_id/skill_name) should be bare: %+v", bare)
	}
	// AddedAt is always populated by InsertAgentKnownSkill regardless of
	// which path created the row — it must not affect bareness.
	bareWithAddedAt := AgentKnownSkill{AgentID: "a1", SkillName: "s1", AddedAt: "2026-08-21T00:00:00Z"}
	if !bareWithAddedAt.IsBareAssignment() {
		t.Errorf("row with only AddedAt set should still be bare: %+v", bareWithAddedAt)
	}

	cases := []struct {
		name string
		row  AgentKnownSkill
	}{
		{"pinned", AgentKnownSkill{Pinned: true}},
		{"activation_count", AgentKnownSkill{ActivationCount: 1}},
		{"last_used_at", AgentKnownSkill{LastUsedAt: "2026-08-21T00:00:00Z"}},
		{"ttl_seconds", AgentKnownSkill{TTLSeconds: 600}},
		{"reason", AgentKnownSkill{Reason: "operator pin"}},
		{"approved_content_hash", AgentKnownSkill{ApprovedContentHash: "skl-vendor-deadbeef"}},
		{"granted_at", AgentKnownSkill{GrantedAt: "2026-08-21T00:00:00Z"}},
		{"granted_by", AgentKnownSkill{GrantedBy: "operator"}},
		{"capabilities_granted", AgentKnownSkill{CapabilitiesGranted: `{"read":true}`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.row.IsBareAssignment() {
				t.Errorf("row with %s set should not be bare: %+v", tc.name, tc.row)
			}
		})
	}
}
