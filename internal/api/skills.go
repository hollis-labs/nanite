package api

import (
	"net/http"
	"os"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
)

// devModeEnabled returns true when either the NANITE_DEVMODE env var is set
// to a truthy value (1/true/yes/on) or the user_settings.developer_mode flag
// is enabled. E1 (CW-20260428-0016) gates the inline-edit affordances on
// internal skills behind this check.
func (a *API) devModeEnabled(r *http.Request) bool {
	if envDevModeOn() {
		return true
	}
	if a.Services.Store == nil {
		return false
	}
	us, err := a.Services.Store.GetUserSettings()
	if err != nil || us == nil {
		return false
	}
	return us.DeveloperMode
}

func envDevModeOn() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("NANITE_DEVMODE")))
	switch v {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func (a *API) handleListSkills(w http.ResponseWriter, r *http.Request) {
	var skills []store.Skill
	var err error

	if source := r.URL.Query().Get("source"); source != "" {
		skills, err = a.Services.Skills.ListBySource(r.Context(), source)
	} else {
		skills, err = a.Services.Skills.List(r.Context())
	}
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, skills)
}

func (a *API) handleCreateSkill(w http.ResponseWriter, r *http.Request) {
	var req CreateSkillRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Name == "" || req.Slug == "" {
		a.errorResp(w, http.StatusBadRequest, "name and slug are required")
		return
	}

	sk := &store.Skill{
		Name:                 req.Name,
		Slug:                 req.Slug,
		Description:          req.Description,
		Category:             req.Category,
		Icon:                 req.Icon,
		InputSchema:          req.InputSchema,
		SourceTier:           req.SourceTier,
		ContentHash:          req.ContentHash,
		DeclaredDependencies: req.DeclaredDependencies,
		Enabled:              true,
	}
	if req.Enabled != nil {
		sk.Enabled = *req.Enabled
	}
	if err := a.Services.Skills.Create(r.Context(), sk); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, sk)
}

func (a *API) handleGetSkill(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sk, err := a.Services.Skills.Get(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sk == nil {
		a.errorResp(w, http.StatusNotFound, "skill not found")
		return
	}
	a.jsonResp(w, http.StatusOK, sk)
}

func (a *API) handleUpdateSkill(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	existing, err := a.Services.Skills.Get(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		a.errorResp(w, http.StatusNotFound, "skill not found")
		return
	}

	var req UpdateSkillRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.Slug != nil {
		existing.Slug = *req.Slug
	}
	if req.Description != nil {
		existing.Description = *req.Description
	}
	if req.Category != nil {
		existing.Category = *req.Category
	}
	if req.Icon != nil {
		existing.Icon = *req.Icon
	}
	if req.InputSchema != nil {
		existing.InputSchema = *req.InputSchema
	}
	if req.SourceTier != nil {
		existing.SourceTier = *req.SourceTier
	}
	if req.ContentHash != nil {
		existing.ContentHash = *req.ContentHash
	}
	if req.DeclaredDependencies != nil {
		existing.DeclaredDependencies = *req.DeclaredDependencies
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}

	if err := a.Services.Skills.Update(r.Context(), existing); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, existing)
}

func (a *API) handleDeleteSkill(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Services.Skills.Delete(r.Context(), id); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (a *API) handleListAgentSkills(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	skills, err := a.Services.Store.ListAgentSkills(agentID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, skills)
}

func (a *API) handleAssignAgentSkill(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")

	var req AssignAgentSkillRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.SkillID == "" {
		a.errorResp(w, http.StatusBadRequest, "skill_id is required")
		return
	}

	// Verify agent exists as a real agent_profiles DB row directly against
	// the store (a.Services.Agents.Get is equivalent post-TASKS/adhoc/01-
	// eliminate-file-based-agent-runtime.md -- it is a plain DB passthrough
	// now too -- but this direct call is kept as the explicit, load-bearing
	// check: AssignSkillToAgent now writes through agent_known_skills
	// (TASKS/skills/02 -- the old, now-dropped per-agent skill join table
	// used to carry this FK instead), which carries the same real FK to
	// agent_profiles(id), so the existence check here must match what the
	// FK actually enforces, independent of whatever AgentService does).
	if _, err := a.Services.Store.GetAgent(agentID); err != nil {
		a.errorResp(w, http.StatusNotFound, "agent not found")
		return
	}
	// Verify skill exists.
	sk, err := a.Services.Skills.Get(r.Context(), req.SkillID)
	if err != nil || sk == nil {
		a.errorResp(w, http.StatusNotFound, "skill not found")
		return
	}

	if err := a.Services.Store.AssignSkillToAgent(agentID, req.SkillID, req.Config); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	skills, err := a.Services.Store.ListAgentSkills(agentID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, skills)
}

func (a *API) handleRemoveAgentSkill(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	skillID := r.PathValue("skillId")

	if err := a.Services.Store.RemoveSkillFromAgent(agentID, skillID); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "removed"})
}

// handleGetDevMode returns whether the dev-mode editor affordances are
// active. E1 (CW-20260428-0016): the FE checks this on render to decide
// whether to surface the "edit internal skill" path.
func (a *API) handleGetDevMode(w http.ResponseWriter, r *http.Request) {
	a.jsonResp(w, http.StatusOK, map[string]any{
		"dev_mode": a.devModeEnabled(r),
		"env_flag": envDevModeOn(),
	})
}

// TASKS/skills/02: handleForkSkillToUser (POST /api/skills/{id}/fork-to-user)
// and ForkSkillToUserRequest are deleted outright, forced by two independent
// facts: (1) store.Skill.Prompt is dropped in this same task (no more
// markdown body on this struct to fork from), and (2) this handler's own
// doc comment described a reload path ("the next discovery cycle... the J7
// AutoIngest pipeline overrides the DB row") that TASKS/skills/01 already
// fully deleted (skill.Discover always returns nil, AutoIngestSkills/
// upsertSkillDef no longer exist) — the feature was already non-functional
// before this task touched it, just not yet noticed. skill.WriteUserSkillFile
// itself is left untouched (internal/skill/loader.go) — TASKS/skills/01 kept
// it as an independently-useful primitive with its own test coverage
// (internal/skill/loader_test.go), out of this task's scope.
