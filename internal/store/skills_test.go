package store

import "testing"

// TestListAgentSkills_ColumnAlignment is a regression guard for the audit
// finding where the SELECT column list was missing `icon`, causing Scan to
// misalign Settings/CreatedAt/UpdatedAt. A known Icon value is round-tripped
// to verify the field mapping is correct.
func TestListAgentSkills_ColumnAlignment(t *testing.T) {
	s := newTestStore(t)
	agent := makeTestAgent(t, s, "skills-align")

	sk := &Skill{
		Name:         "Custom Skill",
		Slug:         "custom-align",
		Description:  "desc",
		Category:     "test",
		ToolBindings: `["tool_a"]`,
		Icon:         "sparkle",
		Settings:     `{"k":"v"}`,
	}
	if err := s.CreateSkill(sk); err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}

	if err := s.AssignSkillToAgent(agent.ID, sk.ID, ""); err != nil {
		t.Fatalf("AssignSkillToAgent: %v", err)
	}

	list, err := s.ListAgentSkills(agent.ID)
	if err != nil {
		t.Fatalf("ListAgentSkills: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(list))
	}
	got := list[0]

	if got.ID != sk.ID {
		t.Errorf("ID mismatch: got %q, want %q", got.ID, sk.ID)
	}
	if got.Slug != "custom-align" {
		t.Errorf("Slug misaligned: got %q", got.Slug)
	}
	if got.Icon != "sparkle" {
		t.Errorf("Icon misaligned: got %q, want %q", got.Icon, "sparkle")
	}
	if got.Settings != `{"k":"v"}` {
		t.Errorf("Settings misaligned: got %q", got.Settings)
	}
	if got.CreatedAt == "" {
		t.Error("CreatedAt should be populated")
	}
	if got.UpdatedAt == "" {
		t.Error("UpdatedAt should be populated")
	}
}
