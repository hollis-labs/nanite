package api

import (
	"net/http"

	"github.com/hollis-labs/nanite/internal/store"
)

// Roles CRUD -- Phase 1 item 01
// (TASKS/phase-1/01-add-roles-table-and-cascade-resolution.md). roles is
// DB-authoritative from creation; there is no file/YAML import path to
// mirror here (contrast with Skills' fork-to-user affordance).

// handleListRoles returns all roles.
// GET /api/roles
func (a *API) handleListRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := a.Services.Store.ListRoles()
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, roles)
}

// handleCreateRole creates a new role.
// POST /api/roles
func (a *API) handleCreateRole(w http.ResponseWriter, r *http.Request) {
	var req CreateRoleRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Name == "" || req.Slug == "" {
		a.errorResp(w, http.StatusBadRequest, "name and slug are required")
		return
	}

	role := &store.Role{
		Slug:               req.Slug,
		Name:               req.Name,
		SystemPrompt:       req.SystemPrompt,
		DefaultClass:       req.DefaultClass,
		DefaultModel:       req.DefaultModel,
		DefaultProvider:    req.DefaultProvider,
		DefaultTools:       req.DefaultTools,
		DefaultSkills:      req.DefaultSkills,
		DefaultPermissions: req.DefaultPermissions,
	}
	if err := a.Services.Store.CreateRole(role); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, role)
}

// handleGetRole returns a single role by ID.
// GET /api/roles/{id}
func (a *API) handleGetRole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	role, err := a.Services.Store.GetRole(id)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if role == nil {
		a.errorResp(w, http.StatusNotFound, "role not found")
		return
	}
	a.jsonResp(w, http.StatusOK, role)
}

// handleUpdateRole updates a role's mutable fields.
// PUT /api/roles/{id}
func (a *API) handleUpdateRole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	existing, err := a.Services.Store.GetRole(id)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		a.errorResp(w, http.StatusNotFound, "role not found")
		return
	}

	var req UpdateRoleRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.Slug != nil {
		existing.Slug = *req.Slug
	}
	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.SystemPrompt != nil {
		existing.SystemPrompt = *req.SystemPrompt
	}
	if req.DefaultClass != nil {
		existing.DefaultClass = *req.DefaultClass
	}
	if req.DefaultModel != nil {
		existing.DefaultModel = *req.DefaultModel
	}
	if req.DefaultProvider != nil {
		existing.DefaultProvider = *req.DefaultProvider
	}
	if req.DefaultTools != nil {
		existing.DefaultTools = *req.DefaultTools
	}
	if req.DefaultSkills != nil {
		existing.DefaultSkills = *req.DefaultSkills
	}
	if req.DefaultPermissions != nil {
		existing.DefaultPermissions = *req.DefaultPermissions
	}

	if err := a.Services.Store.UpdateRole(existing); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, existing)
}

// handleDeleteRole deletes a role by ID.
// DELETE /api/roles/{id}
func (a *API) handleDeleteRole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Services.Store.DeleteRole(id); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "deleted"})
}
