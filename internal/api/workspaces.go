package api

import (
	"net/http"

	"github.com/hollis-labs/mentat/internal/store"
)

func (a *API) handleListWorkspaces(w http.ResponseWriter, r *http.Request) {
	workspaces, err := a.Store.ListWorkspaces()
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, workspaces)
}

func (a *API) handleCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Icon        string `json:"icon"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.ID == "" || req.Name == "" {
		a.errorResp(w, http.StatusBadRequest, "id and name are required")
		return
	}

	ws := &store.Workspace{
		ID:          req.ID,
		Name:        req.Name,
		Description: req.Description,
		Icon:        req.Icon,
	}
	if err := a.Store.CreateWorkspace(ws); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, ws)
}

func (a *API) handleGetWorkspace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ws, err := a.Store.GetWorkspace(id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "workspace not found")
		return
	}
	a.jsonResp(w, http.StatusOK, ws)
}

func (a *API) handleUpdateWorkspace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	existing, err := a.Store.GetWorkspace(id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "workspace not found")
		return
	}

	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Icon        *string `json:"icon"`
	}
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
	if req.Icon != nil {
		existing.Icon = *req.Icon
	}

	if err := a.Store.UpdateWorkspace(existing); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, existing)
}

func (a *API) handleDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Store.DeleteWorkspace(id); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"deleted": id})
}

func (a *API) handleListProjects(w http.ResponseWriter, r *http.Request) {
	wid := r.PathValue("wid")
	projects, err := a.Store.ListProjects(wid)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, projects)
}

func (a *API) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	wid := r.PathValue("wid")

	var req struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		RepoPath    string `json:"repo_path"`
	}
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
		WorkspaceID: wid,
		Name:        req.Name,
		Description: req.Description,
		RepoPath:    req.RepoPath,
	}
	if err := a.Store.CreateProject(p); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, p)
}
