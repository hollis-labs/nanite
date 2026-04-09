package api

import (
	"net/http"

	"github.com/hollis-labs/nanite/internal/store"
)

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

	// Verify agent exists.
	if _, err := a.Services.Agents.Get(r.Context(), agentID); err != nil {
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
