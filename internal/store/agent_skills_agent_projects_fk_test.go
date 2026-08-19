package store

import "testing"

// TestAgentSkills_FKRejectsOrphanedAgentID is the "Done means" verification
// for Phase 1 #05 (migration 107): agent_skills.agent_id must carry a real,
// enforced FK to agent_profiles(id) — inserting a row for an agent_id with
// no backing agent_profiles row must be rejected at the DB level.
func TestAgentSkills_FKRejectsOrphanedAgentID(t *testing.T) {
	s := newTestStore(t)

	sk := &Skill{Name: "FK Test Skill", Slug: "fk-test-skill"}
	if err := s.CreateSkill(sk); err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}

	if err := s.AssignSkillToAgent("agent-does-not-exist", sk.ID, ""); err == nil {
		t.Fatal("expected AssignSkillToAgent to fail for a nonexistent agent_id, got nil error")
	}
}

// TestAgentSkills_FKCascadesOnAgentDelete confirms the new ON DELETE CASCADE
// half of the FK: deleting the parent agent_profiles row removes its
// agent_skills rows too (independent of DeleteAgent's own belt-and-
// suspenders explicit cleanup line).
func TestAgentSkills_FKCascadesOnAgentDelete(t *testing.T) {
	s := newTestStore(t)
	agent := makeTestAgent(t, s, "fk-cascade-skills")

	sk := &Skill{Name: "Cascade Skill", Slug: "fk-cascade-skill"}
	if err := s.CreateSkill(sk); err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}
	if err := s.AssignSkillToAgent(agent.ID, sk.ID, ""); err != nil {
		t.Fatalf("AssignSkillToAgent: %v", err)
	}

	if _, err := s.DB.Exec("DELETE FROM agent_profiles WHERE id = ?", agent.ID); err != nil {
		t.Fatalf("delete agent_profiles row directly: %v", err)
	}

	list, err := s.ListAgentSkills(agent.ID)
	if err != nil {
		t.Fatalf("ListAgentSkills: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected agent_skills row to be cascade-deleted, got %d remaining", len(list))
	}
}

// TestAgentProjects_FKRejectsOrphanedAgentID mirrors
// TestAgentSkills_FKRejectsOrphanedAgentID for agent_projects.
func TestAgentProjects_FKRejectsOrphanedAgentID(t *testing.T) {
	s := newTestStore(t)

	ws := &Workspace{ID: "ws-fk-test-projects", Name: "FK Test Workspace"}
	if err := s.CreateWorkspace(ws); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	proj := &Project{ID: "proj-fk-test", WorkspaceID: ws.ID, Name: "FK Test Project"}
	if err := s.CreateProject(proj); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if err := s.AddAgentProject("agent-does-not-exist", proj.ID); err == nil {
		t.Fatal("expected AddAgentProject to fail for a nonexistent agent_id, got nil error")
	}
}

// TestAgentProjects_FKCascadesOnAgentDelete mirrors
// TestAgentSkills_FKCascadesOnAgentDelete for agent_projects.
func TestAgentProjects_FKCascadesOnAgentDelete(t *testing.T) {
	s := newTestStore(t)
	agent := makeTestAgent(t, s, "fk-cascade-projects")

	ws := &Workspace{ID: "ws-fk-cascade-projects", Name: "FK Cascade Workspace"}
	if err := s.CreateWorkspace(ws); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	proj := &Project{ID: "proj-fk-cascade", WorkspaceID: ws.ID, Name: "FK Cascade Project"}
	if err := s.CreateProject(proj); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := s.AddAgentProject(agent.ID, proj.ID); err != nil {
		t.Fatalf("AddAgentProject: %v", err)
	}

	if _, err := s.DB.Exec("DELETE FROM agent_profiles WHERE id = ?", agent.ID); err != nil {
		t.Fatalf("delete agent_profiles row directly: %v", err)
	}

	list, err := s.ListAgentProjects(agent.ID)
	if err != nil {
		t.Fatalf("ListAgentProjects: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected agent_projects row to be cascade-deleted, got %d remaining", len(list))
	}
}
