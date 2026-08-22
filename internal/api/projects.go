package api

import (
	"net/http"

	"github.com/hollis-labs/nanite/internal/store"
)

// Phase 0 item 20 (TASKS/phase-0/20-retire-workspaces-and-instance-mechanism.md):
// the in-app `workspaces` table (and its 5 CRUD handlers, formerly in this
// file) is retired in full. `projects` is no longer nested under a
// workspace — these handlers are the flat /api/projects surface the
// frontend project managers (WorkspaceProjectManager.tsx, ScopeSelector.tsx,
// NewProjectDialog.tsx, CreateProjectModal.tsx) actually call.

func (a *API) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := a.Services.Store.ListProjects(r.Context())
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, projects)
}

func (a *API) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req CreateProjectRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.ID == "" || req.Name == "" {
		a.errorResp(w, http.StatusBadRequest, "id and name are required")
		return
	}

	p := &store.Project{
		ID:          req.ID,
		Name:        req.Name,
		Description: req.Description,
		RepoPath:    req.RepoPath,
	}
	if err := a.Services.Store.CreateProject(r.Context(), p); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, p)
}

func (a *API) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")

	existing, err := a.Services.Store.GetProject(r.Context(), pid)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "project not found")
		return
	}

	var req UpdateProjectRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.Description != nil {
		existing.Description = *req.Description
	}
	if req.RepoPath != nil {
		existing.RepoPath = *req.RepoPath
	}
	if req.Settings != nil {
		existing.Settings = *req.Settings
	}
	if req.SortOrder != nil {
		existing.SortOrder = *req.SortOrder
	}

	if err := a.Services.Store.UpdateProject(r.Context(), existing); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, existing)
}

func (a *API) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")

	if _, err := a.Services.Store.GetProject(r.Context(), pid); err != nil {
		a.errorResp(w, http.StatusNotFound, "project not found")
		return
	}

	if err := a.Services.Store.DeleteProject(r.Context(), pid); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"deleted": pid})
}
