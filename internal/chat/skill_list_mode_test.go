package chat

import (
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// E2 (CW-20260428-0017): the dispatch-time skill list MUST honor the
// two-pass mode filter when the session has a current_mode_id.

func TestBuildSkillListForSession_BackCompatNoMode(t *testing.T) {
	s := newTestStoreForChat(t)
	agent := mustCreateAgent(t, s, "agent-a")
	sk1 := mustCreateSkill(t, s, &store.Skill{Name: "Plan Skill", Slug: "plan-skill", Description: "plan", ToolBindings: `[]`, ModeIDs: `[]`})
	sk2 := mustCreateSkill(t, s, &store.Skill{Name: "Work Skill", Slug: "work-skill", Description: "work", ToolBindings: `[]`, ModeIDs: `[]`})
	mustAssignSkill(t, s, agent.ID, sk1.ID)
	mustAssignSkill(t, s, agent.ID, sk2.ID)

	// Empty sessionID → legacy behavior: both skills present.
	got := buildSkillListForSession(s, agent.ID, "")
	if !strings.Contains(got, "Plan Skill") || !strings.Contains(got, "Work Skill") {
		t.Fatalf("expected both skills in list, got: %q", got)
	}
}

func TestBuildSkillListForSession_FiltersByMode(t *testing.T) {
	s := newTestStoreForChat(t)
	planMode := mustCreateMode(t, s, "plan")
	workMode := mustCreateMode(t, s, "work")
	agent := mustCreateAgent(t, s, "agent-b")
	planSkill := mustCreateSkill(t, s, &store.Skill{
		Name: "Plan Skill", Slug: "plan-skill", Description: "plan-only", ToolBindings: `[]`,
		ModeIDs: store.MarshalSkillModeIDs([]string{planMode.ID}),
	})
	workSkill := mustCreateSkill(t, s, &store.Skill{
		Name: "Work Skill", Slug: "work-skill", Description: "work-only", ToolBindings: `[]`,
		ModeIDs: store.MarshalSkillModeIDs([]string{workMode.ID}),
	})
	universal := mustCreateSkill(t, s, &store.Skill{
		Name: "Universal", Slug: "universal", Description: "all modes", ToolBindings: `[]`,
		ModeIDs: `[]`,
	})
	mustAssignSkill(t, s, agent.ID, planSkill.ID)
	mustAssignSkill(t, s, agent.ID, workSkill.ID)
	mustAssignSkill(t, s, agent.ID, universal.ID)

	// Session pointing at plan mode.
	sess := mustCreateSession(t, s)
	if err := s.SetSessionMode(sess.ID, planMode.ID); err != nil {
		t.Fatalf("SetSessionMode: %v", err)
	}

	got := buildSkillListForSession(s, agent.ID, sess.ID)
	if !strings.Contains(got, "Plan Skill") {
		t.Errorf("plan skill missing: %q", got)
	}
	if !strings.Contains(got, "Universal") {
		t.Errorf("universal skill missing: %q", got)
	}
	if strings.Contains(got, "Work Skill") {
		t.Errorf("work skill should not be present in plan mode: %q", got)
	}
}

func TestBuildSkillListForSession_DenyOverridesModeMatch(t *testing.T) {
	s := newTestStoreForChat(t)
	planMode := mustCreateMode(t, s, "plan")
	// Assign a deny override to plan mode that excludes "writing-skill".
	planMode.ToolOverrides = `{"deny":["writing-skill"]}`
	if err := s.UpdateMode(planMode); err != nil {
		t.Fatalf("UpdateMode: %v", err)
	}

	agent := mustCreateAgent(t, s, "agent-c")
	denied := mustCreateSkill(t, s, &store.Skill{
		Name: "Writing", Slug: "writing-skill", Description: "deny me", ToolBindings: `[]`,
		ModeIDs: store.MarshalSkillModeIDs([]string{planMode.ID}),
	})
	allowed := mustCreateSkill(t, s, &store.Skill{
		Name: "Drafting", Slug: "drafting-skill", Description: "keep me", ToolBindings: `[]`,
		ModeIDs: store.MarshalSkillModeIDs([]string{planMode.ID}),
	})
	mustAssignSkill(t, s, agent.ID, denied.ID)
	mustAssignSkill(t, s, agent.ID, allowed.ID)

	sess := mustCreateSession(t, s)
	if err := s.SetSessionMode(sess.ID, planMode.ID); err != nil {
		t.Fatalf("SetSessionMode: %v", err)
	}

	got := buildSkillListForSession(s, agent.ID, sess.ID)
	if strings.Contains(got, "Writing") {
		t.Errorf("Writing should be denied: %q", got)
	}
	if !strings.Contains(got, "Drafting") {
		t.Errorf("Drafting should be present: %q", got)
	}
}

// --- helpers ---

func mustCreateAgent(t *testing.T, s *store.Store, slug string) *store.AgentProfile {
	t.Helper()
	a := &store.AgentProfile{
		Slug:        slug,
		Name:        slug,
		Description: "test agent",
		Source:      "user",
	}
	if err := s.CreateAgent(a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return a
}

func mustCreateSkill(t *testing.T, s *store.Store, sk *store.Skill) *store.Skill {
	t.Helper()
	if sk.ToolBindings == "" {
		sk.ToolBindings = "[]"
	}
	if err := s.CreateSkill(sk); err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}
	return sk
}

func mustAssignSkill(t *testing.T, s *store.Store, agentID, skillID string) {
	t.Helper()
	if err := s.AssignSkillToAgent(agentID, skillID, ""); err != nil {
		t.Fatalf("AssignSkillToAgent: %v", err)
	}
}

func mustCreateMode(t *testing.T, s *store.Store, slug string) *store.Mode {
	t.Helper()
	m := &store.Mode{Slug: slug, Name: slug}
	if err := s.CreateMode(m); err != nil {
		t.Fatalf("CreateMode: %v", err)
	}
	return m
}

func mustCreateSession(t *testing.T, s *store.Store) *store.Session {
	t.Helper()
	sess := &store.Session{
		Title: "test-session",
	}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return sess
}
