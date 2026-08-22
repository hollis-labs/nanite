package store

import (
	"context"
	"errors"
	"testing"
)

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

// TestRemoveSkillFromAgent_DeletesBareAssignment is the "row is truly bare"
// half of TASKS/skills/02's fix-required regression coverage: a plain
// AssignSkillToAgent assignment, with no known-skill grant/telemetry data
// ever layered onto it via the separate /known-skills REST surface, is
// physically removed by RemoveSkillFromAgent exactly as before this fix.
func TestRemoveSkillFromAgent_DeletesBareAssignment(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "remove-bare-assignment")

	sk := &Skill{Name: "Bare Assignment Skill", Slug: "bare-assignment-skill"}
	if err := s.CreateSkill(sk); err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}
	if err := s.AssignSkillToAgent(agent.ID, sk.ID, ""); err != nil {
		t.Fatalf("AssignSkillToAgent: %v", err)
	}

	if err := s.RemoveSkillFromAgent(agent.ID, sk.ID); err != nil {
		t.Fatalf("RemoveSkillFromAgent: %v", err)
	}

	if _, err := s.GetAgentKnownSkill(ctx, agent.ID, sk.Slug); !errors.Is(err, ErrAgentKnownSkillNotFound) {
		t.Fatalf("GetAgentKnownSkill after removing bare assignment: got %v, want ErrAgentKnownSkillNotFound", err)
	}
}

// TestRemoveSkillFromAgent_PreservesKnownSkillGrantData reproduces the fresh
// reviewer's second finding directly (TASKS/skills/02's fix-required
// section, 2026-08-21): a real known-skill grant/telemetry row set via the
// separate /known-skills REST surface (internal/api/agent_capabilities.go)
// must survive a call to RemoveSkillFromAgent (the /skills "unassign"
// endpoint) against the same agent+skill — "assignment" isn't a real column
// on agent_known_skills, just row existence, so unassigning must not destroy
// grant data a different write path owns.
func TestRemoveSkillFromAgent_PreservesKnownSkillGrantData(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "remove-preserves-grant")

	sk := &Skill{Name: "Granted Skill", Slug: "granted-skill"}
	if err := s.CreateSkill(sk); err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}

	// Real known-skill grant data, set the way the Agent Capabilities Panel
	// (or a future task 09 grant workflow) would via InsertAgentKnownSkill
	// directly, not via AssignSkillToAgent's bare-row path.
	granted := AgentKnownSkill{
		AgentID:             agent.ID,
		SkillName:           sk.Slug,
		Pinned:              true,
		Reason:              "operator pin",
		ApprovedContentHash: "skl-vendor-deadbeefcafefeed",
		GrantedAt:           "2026-08-21T00:00:00Z",
		GrantedBy:           "operator",
		CapabilitiesGranted: `{"read":true}`,
	}
	if err := s.InsertAgentKnownSkill(ctx, granted); err != nil {
		t.Fatalf("InsertAgentKnownSkill: %v", err)
	}

	// RemoveSkillFromAgent must report success (matching the Wizard's own
	// "unassign" semantics, and the frontend's removeSkillFromAgent call,
	// which surfaces no error UI for this endpoint) without touching the
	// grant data.
	if err := s.RemoveSkillFromAgent(agent.ID, sk.ID); err != nil {
		t.Fatalf("RemoveSkillFromAgent: expected nil (no-op) error, got %v", err)
	}

	got, err := s.GetAgentKnownSkill(ctx, agent.ID, sk.Slug)
	if err != nil {
		t.Fatalf("GetAgentKnownSkill after RemoveSkillFromAgent: %v", err)
	}
	if !got.Pinned || got.Reason != "operator pin" ||
		got.ApprovedContentHash != "skl-vendor-deadbeefcafefeed" ||
		got.GrantedAt != "2026-08-21T00:00:00Z" || got.GrantedBy != "operator" ||
		got.CapabilitiesGranted != `{"read":true}` {
		t.Fatalf("known-skill grant data was not preserved: %+v", got)
	}
}
