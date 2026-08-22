package store

import (
	"context"
	"testing"
)

// TestAgentProjects_FKRejectsOrphanedAgentID pins the "Done means"
// verification for Phase 1 #05 (migration 113): agent_projects.agent_id must
// carry a real, enforced FK to agent_profiles(id) — inserting a row for an
// agent_id with no backing agent_profiles row must be rejected at the DB
// level.
//
// TASKS/skills/02: this file used to also cover the old, dedicated
// agent<->skill join table (the sibling table migration 113 gave the same
// FK treatment). That table is dropped in full by this task —
// docs/engineering/architecture/20-skills.md's "Scope" section and this
// task's own instruction — with no replacement FK-cascade test needed,
// since agent_known_skills (the table AssignSkillToAgent/ListAgentSkills
// are rewired onto) already carries its own `agent_id ... REFERENCES
// agent_profiles(id)` from 069_per_agent_state.sql and is exercised
// elsewhere (agents_test.go's TestDeleteAgent_NoPragmaToggle).
func TestAgentProjects_FKRejectsOrphanedAgentID(t *testing.T) {
	s := newTestStore(t)

	proj := &Project{ID: "proj-fk-test", Name: "FK Test Project"}
	if err := s.CreateProject(context.Background(), proj); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if err := s.AddAgentProject(context.Background(), "agent-does-not-exist", proj.ID); err == nil {
		t.Fatal("expected AddAgentProject to fail for a nonexistent agent_id, got nil error")
	}
}

// TestAgentProjects_FKCascadesOnAgentDelete confirms the ON DELETE CASCADE
// half of the FK: deleting the parent agent_profiles row removes its
// agent_projects rows too (independent of DeleteAgent's own belt-and-
// suspenders explicit cleanup line).
func TestAgentProjects_FKCascadesOnAgentDelete(t *testing.T) {
	s := newTestStore(t)
	agent := makeTestAgent(t, s, "fk-cascade-projects")

	proj := &Project{ID: "proj-fk-cascade", Name: "FK Cascade Project"}
	if err := s.CreateProject(context.Background(), proj); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := s.AddAgentProject(context.Background(), agent.ID, proj.ID); err != nil {
		t.Fatalf("AddAgentProject: %v", err)
	}

	if _, err := s.DB.Exec("DELETE FROM agent_profiles WHERE id = ?", agent.ID); err != nil {
		t.Fatalf("delete agent_profiles row directly: %v", err)
	}

	list, err := s.ListAgentProjects(context.Background(), agent.ID)
	if err != nil {
		t.Fatalf("ListAgentProjects: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected agent_projects row to be cascade-deleted, got %d remaining", len(list))
	}
}
