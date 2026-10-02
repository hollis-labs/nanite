package api

import (
	"errors"
	"net/http"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// Phase 0 item 20 (TASKS/phase-0/20-retire-workspaces-and-instance-mechanism.md):
// the in-app `workspaces` table (and its 5 CRUD handlers, formerly in this
// file) is retired in full. `projects` is no longer nested under a
// workspace — these handlers are the flat /api/projects surface the
// frontend project managers (WorkspaceProjectManager.tsx, ScopeSelector.tsx,
// NewProjectDialog.tsx, CreateProjectModal.tsx) actually call.

func (a *API) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := a.Services.Projects.List(r.Context())
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, projectsToView(projects))
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
	if err := a.Services.Projects.Create(r.Context(), p); err != nil {
		a.projectWriteError(w, err)
		return
	}
	a.jsonResp(w, http.StatusCreated, projectToView(p))
}

func (a *API) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")

	existing, err := a.Services.Projects.Get(r.Context(), pid)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "project not found")
		return
	}

	var req UpdateProjectRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	patch := service.ProjectPatch{
		Name:        req.Name,
		Description: req.Description,
		RepoPath:    req.RepoPath,
		Settings:    req.Settings,
		SortOrder:   req.SortOrder,
	}
	if err := a.Services.Projects.Update(r.Context(), existing, patch); err != nil {
		a.projectWriteError(w, err)
		return
	}
	a.jsonResp(w, http.StatusOK, projectToView(existing))
}

// projectWriteError maps a project create/update error: a rejected field is
// a 400 with the rule's message, anything else a 500.
func (a *API) projectWriteError(w http.ResponseWriter, err error) {
	var ve *service.ProjectValidationError
	if errors.As(err, &ve) {
		a.errorResp(w, http.StatusBadRequest, ve.Msg)
		return
	}
	a.errorResp(w, http.StatusInternalServerError, err.Error())
}

func (a *API) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")

	if err := a.Services.Projects.Delete(r.Context(), pid); err != nil {
		if errors.Is(err, service.ErrProjectNotFound) {
			a.errorResp(w, http.StatusNotFound, "project not found")
			return
		}
		var inUse *service.ProjectInUseError
		if errors.As(err, &inUse) {
			// The sessions in the way, so a client can archive or move them.
			a.jsonResp(w, http.StatusConflict, map[string]any{
				"error":    inUse.Error(),
				"sessions": projectSessionRefsToView(inUse.Sessions),
			})
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"deleted": pid})
}
