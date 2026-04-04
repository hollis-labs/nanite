package api

import (
	"net/http"

	"github.com/hollis-labs/nanite/internal/store"
)

func (a *API) handleListModes(w http.ResponseWriter, r *http.Request) {
	modes, err := a.Services.Store.ListModes()
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, modes)
}

func (a *API) handleGetMode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, err := a.Services.Store.GetMode(id)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if m == nil {
		a.errorResp(w, http.StatusNotFound, "mode not found")
		return
	}
	a.jsonResp(w, http.StatusOK, m)
}

func (a *API) handleCreateMode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name           string `json:"name"`
		Slug           string `json:"slug"`
		PromptAddendum string `json:"prompt_addendum"`
		ToolOverrides  string `json:"tool_overrides"`
		Settings       string `json:"settings"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Name == "" || req.Slug == "" {
		a.errorResp(w, http.StatusBadRequest, "name and slug are required")
		return
	}

	m := &store.Mode{
		Name:           req.Name,
		Slug:           req.Slug,
		PromptAddendum: req.PromptAddendum,
		ToolOverrides:  req.ToolOverrides,
		Settings:       req.Settings,
	}
	if err := a.Services.Store.CreateMode(m); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, m)
}

func (a *API) handleUpdateMode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	existing, err := a.Services.Store.GetMode(id)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		a.errorResp(w, http.StatusNotFound, "mode not found")
		return
	}

	var req struct {
		Name           *string `json:"name"`
		Slug           *string `json:"slug"`
		PromptAddendum *string `json:"prompt_addendum"`
		ToolOverrides  *string `json:"tool_overrides"`
		Settings       *string `json:"settings"`
	}
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
	if req.PromptAddendum != nil {
		existing.PromptAddendum = *req.PromptAddendum
	}
	if req.ToolOverrides != nil {
		existing.ToolOverrides = *req.ToolOverrides
	}
	if req.Settings != nil {
		existing.Settings = *req.Settings
	}

	if err := a.Services.Store.UpdateMode(existing); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, existing)
}

func (a *API) handleDeleteMode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Services.Store.DeleteMode(id); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (a *API) handleAssignModeToAgent(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")

	var req struct {
		ModeID string `json:"mode_id"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.ModeID == "" {
		a.errorResp(w, http.StatusBadRequest, "mode_id is required")
		return
	}

	// Verify agent exists.
	if _, err := a.Services.Store.GetAgent(agentID); err != nil {
		a.errorResp(w, http.StatusNotFound, "agent not found")
		return
	}
	// Verify mode exists.
	m, err := a.Services.Store.GetMode(req.ModeID)
	if err != nil || m == nil {
		a.errorResp(w, http.StatusNotFound, "mode not found")
		return
	}

	if err := a.Services.Store.AssignModeToAgent(agentID, req.ModeID); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	modes, err := a.Services.Store.GetAgentAssignedModes(agentID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, modes)
}

func (a *API) handleUnassignModeFromAgent(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	modeID := r.PathValue("modeId")

	if err := a.Services.Store.UnassignModeFromAgent(agentID, modeID); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (a *API) handleListAgentAssignedModes(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	modes, err := a.Services.Store.GetAgentAssignedModes(agentID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, modes)
}
