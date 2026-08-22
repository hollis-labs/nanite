package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestHandleAssignAgentSkill_RejectsNonexistentAgent is a regression pin for
// Phase 1 #05's precondition-verification finding: assigning a skill to an
// agent ID that has never existed anywhere (no DB row, no file definition)
// must be rejected with 404, not silently inserted. TASKS/skills/02:
// AssignSkillToAgent now writes through agent_known_skills (replacing the
// old, now-dropped per-agent skill join table) — the 404 gate and this
// regression pin are otherwise unchanged.
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
		t.Errorf("expected no agent_known_skills row for a rejected assignment, got %d", len(skills))
	}
}

// TestHandleAssignAgentSkill_RejectsFileBasedGhostAgent (the direct
// regression pin for Phase 1 #05's file-discovered-ghost-agent finding) was
// removed by TASKS/adhoc/01-eliminate-file-based-agent-runtime.md: the
// vulnerability it pinned -- a.Services.Agents.Get resolving a purely
// file-discovered agent.Definition (no `id:` frontmatter, canonical ID
// "file-<slug>") via the in-memory fileDefs slice before ever touching the
// DB -- can no longer occur. AgentService.Get/GetBySlug/List are pure DB
// passthroughs now (no fileDefs registry exists to resolve through), and
// handleAssignAgentSkill's own direct a.Services.Store.GetAgent check
// (unchanged by this task) is still the load-bearing 404 gate --
// TestHandleAssignAgentSkill_RejectsNonexistentAgent above already covers
// "no such agent at all" the same way this test would have degenerated to.
