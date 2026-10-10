package store

import (
	"context"
	"os"
	"testing"
)

// CW-20260929-0019: migration 167 removes grants for retired skills that have
// no catalog row, and nothing else.
func TestMigration167_RetiresDanglingSkillGrants(t *testing.T) {
	ctx := context.Background()
	s := newSeededStore(t)
	agent := &AgentProfile{Name: "Grantee", Slug: "grantee", SystemPrompt: "x"}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO agent_profiles(id,name,slug,system_prompt) VALUES(?,?,?,?)`, "historical-"+agent.Slug, agent.Name, agent.Slug, agent.SystemPrompt); err != nil {
		t.Fatal(err)
	}
	agent.ID = "historical-" + agent.Slug
	// A real, installed skill, and one whose slug is on the retire list but IS
	// installed (its grant must survive).
	for _, slug := range []string{"capture-decision", "escalate"} {
		sk := &Skill{Name: slug, Slug: slug, Description: "d"}
		if err := s.CreateSkill(ctx, sk); err != nil {
			t.Fatal(err)
		}
	}
	grant := func(slug string) {
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO agent_known_skills (agent_id, skill_name) VALUES (?, ?)`, agent.ID, slug); err != nil {
			t.Fatalf("grant %s: %v", slug, err)
		}
	}
	retired := []string{"sp-brainstorming", "sp-writing-plans", "sp-systematic-debugging", "sp-verification-before-completion", "dispatching-parallel-agents"}
	for _, slug := range retired {
		grant(slug)
	}
	// left alone: adr and capture-to-vanta (need a decision), an installed skill,
	// and an installed skill whose slug is on the retire list.
	for _, slug := range []string{"adr", "capture-to-vanta", "capture-decision", "escalate"} {
		grant(slug)
	}

	sql, err := os.ReadFile("migrations/167_retire_dangling_skill_grants.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // idempotent
		if _, execErr := s.DB.ExecContext(ctx, string(sql)); execErr != nil {
			t.Fatalf("run %d: %v", i+1, execErr)
		}
	}

	rows, err := s.DB.QueryContext(ctx, `SELECT skill_name FROM agent_known_skills WHERE agent_id = ? ORDER BY skill_name`, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		got = append(got, n)
	}
	want := []string{"adr", "capture-decision", "capture-to-vanta", "escalate"}
	if len(got) != len(want) {
		t.Fatalf("remaining grants = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("remaining grants = %v, want %v", got, want)
		}
	}
}

func TestListDanglingSkillGrants(t *testing.T) {
	ctx := context.Background()
	s := newSeededStore(t)
	agent := makeTestAgent(t, s, "dangling-grants")
	if err := s.CreateSkill(ctx, &Skill{Name: "real", Slug: "real", Description: "d"}); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"real", "ghost-b", "ghost-a"} {
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO actor_known_skills (agent_id, skill_name) VALUES (?, ?)`, agent.ID, slug); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListDanglingSkillGrants(ctx, agent.ID)
	if err != nil || len(got) != 2 || got[0] != "ghost-a" || got[1] != "ghost-b" {
		t.Errorf("dangling = %v, %v; want [ghost-a ghost-b]", got, err)
	}
	if got, _ := s.ListDanglingSkillGrants(ctx, "nobody"); len(got) != 0 {
		t.Errorf("unknown agent dangling = %v", got)
	}
}
