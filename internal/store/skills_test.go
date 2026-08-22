package store

import "testing"

// TestListAgentSkills_ColumnAlignment is a regression guard for the audit
// finding where the SELECT column list was missing `icon`, causing Scan to
// misalign later columns. A known Icon value is round-tripped to verify the
// field mapping is correct.
//
// TASKS/skills/02: rewritten against the redesigned, index-only Skill shape
// (ToolBindings/Settings/IsBuiltin/Prompt/ModeIDs are gone) and against
// AssignSkillToAgent/ListAgentSkills's new agent_known_skills-backed
// implementation (the old, dedicated agent<->skill join table is dropped —
// see internal/store/skills.go's ListAgentSkills doc comment for its
// history).
func TestListAgentSkills_ColumnAlignment(t *testing.T) {
	s := newTestStore(t)
	agent := makeTestAgent(t, s, "skills-align")

	sk := &Skill{
		Name:                 "Custom Skill",
		Slug:                 "custom-align",
		Description:          "desc",
		Category:             "test",
		Icon:                 "sparkle",
		SourceTier:           "user",
		ContentHash:          "skl-vendor-deadbeefcafefeed",
		DeclaredDependencies: `["other-skill"]`,
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
	if got.SourceTier != "user" {
		t.Errorf("SourceTier misaligned: got %q", got.SourceTier)
	}
	if got.ContentHash != "skl-vendor-deadbeefcafefeed" {
		t.Errorf("ContentHash misaligned: got %q", got.ContentHash)
	}
	if got.DeclaredDependencies != `["other-skill"]` {
		t.Errorf("DeclaredDependencies misaligned: got %q", got.DeclaredDependencies)
	}
	if got.Version != 1 {
		t.Errorf("Version misaligned: got %d, want 1", got.Version)
	}
	if got.InstalledAt == "" {
		t.Error("InstalledAt should be populated")
	}
	if got.UpdatedAt == "" {
		t.Error("UpdatedAt should be populated")
	}
}

// TestAssignSkillToAgent_RejectsOrphanedAgentID is TASKS/skills/02's
// replacement for the old, dropped agent<->skill join table's own
// FK-rejection coverage (formerly agent_skills_agent_projects_fk_test.go's
// TestAgentSkills_FKRejectsOrphanedAgentID, now agent_projects_fk_test.go
// post-rename): AssignSkillToAgent now writes through agent_known_skills,
// which still carries `agent_id ... REFERENCES agent_profiles(id)`
// (069_per_agent_state.sql) — an assignment against a nonexistent agent_id
// must still fail.
//
// No replacement for the old join table's ON-DELETE-CASCADE coverage
// (formerly TestAgentSkills_FKCascadesOnAgentDelete) is added here:
// agent_known_skills' own `REFERENCES agent_profiles(id)` (069_per_agent_
// state.sql) carries no `ON DELETE CASCADE` clause, so a raw
// `DELETE FROM agent_profiles` while a known-skill row still references it
// is rejected at the DB level (empirically confirmed against a scratch
// sqlite3 DB during this task), not cascaded — unlike the old, FK-rebuilt
// join table (113_agent_skills_agent_projects_fk.sql). Cleanup on agent
// deletion is DeleteAgent's own explicit responsibility
// (internal/store/agents.go), already covered by agents_test.go's
// TestDeleteAgent_NoPragmaToggle.
func TestAssignSkillToAgent_RejectsOrphanedAgentID(t *testing.T) {
	s := newTestStore(t)

	sk := &Skill{Name: "FK Test Skill", Slug: "fk-test-skill"}
	if err := s.CreateSkill(sk); err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}

	if err := s.AssignSkillToAgent("agent-does-not-exist", sk.ID, ""); err == nil {
		t.Fatal("expected AssignSkillToAgent to fail for a nonexistent agent_id, got nil error")
	}
}
