package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestHandleAssignAgentSkill_RejectsNonexistentAgent is a regression pin for
// Phase 1 #05's precondition-verification finding: assigning a skill to an
// agent ID that has never existed anywhere (no DB row, no file definition)
// must be rejected with 404, not silently inserted into agent_skills.
func TestHandleAssignAgentSkill_RejectsNonexistentAgent(t *testing.T) {
	a, mux := newTestAPI(t)

	sk := &store.Skill{Name: "Ghost Skill", Slug: "ghost-skill-nonexistent-agent"}
	if err := a.Services.Skills.Create(context.Background(), sk); err != nil {
		t.Fatalf("create skill: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"skill_id": sk.ID})
	req := httptest.NewRequest("POST", "/api/agents/does-not-exist/skills", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("POST /api/agents/{id}/skills for nonexistent agent: expected 404, got %d; body: %s", w.Code, w.Body.String())
	}

	skills, err := a.Services.Store.ListAgentSkills("does-not-exist")
	if err != nil {
		t.Fatalf("ListAgentSkills: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("expected no agent_skills row for a rejected assignment, got %d", len(skills))
	}
}

// TestHandleAssignAgentSkill_RejectsFileBasedGhostAgent is the direct
// regression pin for the gap found during Phase 1 #05's independent
// verification: a purely file-discovered agent definition (no `id:`
// frontmatter, canonical ID "file-<slug>") that AutoIngestAgents has never
// (or has failed to) write a backing agent_profiles row for is exactly the
// class of agent the old check (a.Services.Agents.Get, which resolves file
// definitions from the in-memory fileDefs slice before ever touching the DB)
// would wrongly treat as "exists". The handler must now use a direct DB-row
// check (a.Services.Store.GetAgent) and reject this case with 404.
func TestHandleAssignAgentSkill_RejectsFileBasedGhostAgent(t *testing.T) {
	a, mux := newTestAPI(t)

	ghostDef := &agent.Definition{
		Name:         "Ghost",
		Slug:         "ghost-agent-no-db-row",
		SystemPrompt: "unused",
		Source:       "project",
	}
	// Override the agent service with one that knows about ghostDef purely
	// as a file definition -- mirrors the exact shape AutoIngestAgents'
	// per-definition-failure gap leaves behind (visible via file discovery,
	// no backing agent_profiles row). Uses the same real store for the
	// AgentReader/AgentWriter halves so a.Services.Store.GetAgent still sees
	// a real, empty agent_profiles table.
	a.Services.Agents = service.NewAgentService(service.AgentServiceConfig{
		Agents:     a.Services.Store,
		Writers:    a.Services.Store,
		FileAgents: []*agent.Definition{ghostDef},
	})

	ghostID := ghostDef.CanonicalID()
	if ghostID != "file-ghost-agent-no-db-row" {
		t.Fatalf("sanity check: unexpected canonical ID %q", ghostID)
	}

	// Sanity: confirm the old check (AgentService.Get) DOES resolve this
	// agent -- proving the vulnerability the fix closes was real, not just
	// theoretical.
	if _, err := a.Services.Agents.Get(context.Background(), ghostID); err != nil {
		t.Fatalf("sanity check: AgentService.Get should resolve the file-based ghost agent, got err: %v", err)
	}
	// And confirm there is genuinely no backing DB row.
	if _, err := a.Services.Store.GetAgent(ghostID); err == nil {
		t.Fatalf("sanity check: expected no agent_profiles row for %q, GetAgent succeeded", ghostID)
	}

	sk := &store.Skill{Name: "Ghost Skill 2", Slug: "ghost-skill-file-based-agent"}
	if err := a.Services.Skills.Create(context.Background(), sk); err != nil {
		t.Fatalf("create skill: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"skill_id": sk.ID})
	req := httptest.NewRequest("POST", "/api/agents/"+ghostID+"/skills", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("POST /api/agents/{id}/skills for file-based ghost agent: expected 404, got %d; body: %s", w.Code, w.Body.String())
	}

	skills, err := a.Services.Store.ListAgentSkills(ghostID)
	if err != nil {
		t.Fatalf("ListAgentSkills: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("expected no agent_skills row for the file-based ghost agent, got %d", len(skills))
	}
}
