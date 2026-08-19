package api

import (
	"net/http"
	"os"
	"strings"

	"github.com/hollis-labs/nanite/internal/skill"
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
		Name:         req.Name,
		Slug:         req.Slug,
		Description:  req.Description,
		Category:     req.Category,
		ToolBindings: req.ToolBindings,
		InputSchema:  req.InputSchema,
		Settings:     req.Settings,
		Icon:         req.Icon,
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
	if req.ToolBindings != nil {
		existing.ToolBindings = *req.ToolBindings
	}
	if req.InputSchema != nil {
		existing.InputSchema = *req.InputSchema
	}
	if req.Settings != nil {
		existing.Settings = *req.Settings
	}
	if req.Icon != nil {
		existing.Icon = *req.Icon
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
	// check: agent_skills.agent_id carries a real FK to agent_profiles(id)
	// (Phase 1 #05), so the existence check here must match what the FK
	// actually enforces, independent of whatever AgentService does).
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

// ForkSkillToUserRequest carries the new prompt body for a "fork to user
// override" save. The slug is derived from the URL path; the source skill
// must exist (file-based or DB) and the request must come from a dev-mode
// session — otherwise a 403 is returned.
type ForkSkillToUserRequest struct {
	Prompt string `json:"prompt"`
}

// handleForkSkillToUser writes a markdown skill file at
// ~/.nanite/skills/<slug>.md so the user gets an editable copy of an
// internal skill. The next discovery cycle will pick it up and the J7
// AutoIngest pipeline overrides the DB row with the user version (since
// user-source files take priority over builtin in Discover()).
//
// Refuses without dev mode. Path param {id} accepts either a real skill
// ID (DB UUID) or the deterministic file-based "file-{slug}" form.
func (a *API) handleForkSkillToUser(w http.ResponseWriter, r *http.Request) {
	if !a.devModeEnabled(r) {
		a.errorResp(w, http.StatusForbidden, "dev mode is not enabled — set NANITE_DEVMODE=1 or developer_mode=true")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		a.errorResp(w, http.StatusBadRequest, "skill id is required")
		return
	}
	sk, err := a.Services.Skills.Get(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sk == nil {
		a.errorResp(w, http.StatusNotFound, "skill not found")
		return
	}

	var req ForkSkillToUserRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	body := req.Prompt
	if strings.TrimSpace(body) == "" {
		// Default to the existing prompt when no body is supplied — equivalent
		// to "fork as-is for me to edit later via the file system".
		body = sk.Prompt
	}

	target, err := skill.WriteUserSkillFile("", sk.Slug, body)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResp(w, http.StatusOK, map[string]any{
		"status":     "forked",
		"slug":       sk.Slug,
		"path":       target,
		"reload":     "restart nanite or wait for next discovery cycle to re-ingest",
		"user_owned": true,
	})
}
