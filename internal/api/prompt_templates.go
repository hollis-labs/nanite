package api

import (
	"net/http"

	"github.com/hollis-labs/mentat/internal/store"
)

func (a *API) handleListPromptTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := a.Store.ListPromptTemplates()
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, templates)
}

func (a *API) handleCreatePromptTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		Slug      string `json:"slug"`
		Scope     string `json:"scope"`
		Template  string `json:"template"`
		Variables string `json:"variables"`
		Priority  int    `json:"priority"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Name == "" || req.Slug == "" || req.Template == "" {
		a.errorResp(w, http.StatusBadRequest, "name, slug, and template are required")
		return
	}
	if req.Scope == "" {
		req.Scope = "system"
	}

	pt := &store.PromptTemplate{
		Name:      req.Name,
		Slug:      req.Slug,
		Scope:     req.Scope,
		Template:  req.Template,
		Variables: req.Variables,
		Priority:  req.Priority,
	}
	if err := a.Store.CreatePromptTemplate(pt); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, pt)
}

func (a *API) handleGetPromptTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pt, err := a.Store.GetPromptTemplate(id)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if pt == nil {
		a.errorResp(w, http.StatusNotFound, "prompt template not found")
		return
	}
	a.jsonResp(w, http.StatusOK, pt)
}

func (a *API) handleUpdatePromptTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	existing, err := a.Store.GetPromptTemplate(id)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		a.errorResp(w, http.StatusNotFound, "prompt template not found")
		return
	}

	var req struct {
		Name      *string `json:"name"`
		Slug      *string `json:"slug"`
		Scope     *string `json:"scope"`
		Template  *string `json:"template"`
		Variables *string `json:"variables"`
		Priority  *int    `json:"priority"`
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
	if req.Scope != nil {
		existing.Scope = *req.Scope
	}
	if req.Template != nil {
		existing.Template = *req.Template
	}
	if req.Variables != nil {
		existing.Variables = *req.Variables
	}
	if req.Priority != nil {
		existing.Priority = *req.Priority
	}

	if err := a.Store.UpdatePromptTemplate(existing); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, existing)
}

func (a *API) handleDeletePromptTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Store.DeletePromptTemplate(id); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (a *API) handleListAgentPromptTemplates(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	templates, err := a.Store.ListPromptTemplatesForAgent(agentID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, templates)
}

func (a *API) handleAssignAgentPromptTemplate(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")

	var req struct {
		TemplateID string `json:"template_id"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.TemplateID == "" {
		a.errorResp(w, http.StatusBadRequest, "template_id is required")
		return
	}

	// Verify agent exists.
	if _, err := a.Store.GetAgent(agentID); err != nil {
		a.errorResp(w, http.StatusNotFound, "agent not found")
		return
	}
	// Verify template exists.
	pt, err := a.Store.GetPromptTemplate(req.TemplateID)
	if err != nil || pt == nil {
		a.errorResp(w, http.StatusNotFound, "prompt template not found")
		return
	}

	if err := a.Store.AssignPromptTemplateToAgent(agentID, req.TemplateID); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	templates, err := a.Store.ListPromptTemplatesForAgent(agentID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, templates)
}

func (a *API) handleRemoveAgentPromptTemplate(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	templateID := r.PathValue("templateId")

	if err := a.Store.RemovePromptTemplateFromAgent(agentID, templateID); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "removed"})
}
