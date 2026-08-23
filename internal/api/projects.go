package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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
	repoPath, err := validateProjectRepoPath(req.RepoPath)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid repo_path: "+err.Error())
		return
	}

	p := &store.Project{
		ID:          req.ID,
		Name:        req.Name,
		Description: req.Description,
		RepoPath:    repoPath,
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
		repoPath, err := validateProjectRepoPath(*req.RepoPath)
		if err != nil {
			a.errorResp(w, http.StatusBadRequest, "invalid repo_path: "+err.Error())
			return
		}
		existing.RepoPath = repoPath
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

// validateProjectRepoPath applies AD-27 at the write boundary. Empty means a
// project has no repository attached. Non-empty paths are canonicalized so a
// symlink cannot disguise a forbidden root, must already name a directory,
// and may not expose a whole filesystem, the user's whole home, or a named
// system tree to metadata enumeration through autocomplete.
func validateProjectRepoPath(repoPath string) (string, error) {
	if strings.TrimSpace(repoPath) == "" {
		return "", nil
	}
	canonical, err := canonicalExistingDir(repoPath)
	if err != nil {
		return "", err
	}

	volumeRoot := filepath.Clean(filepath.VolumeName(canonical) + string(filepath.Separator))
	if canonical == volumeRoot {
		return "", fmt.Errorf("filesystem root %q is not allowed", canonical)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	canonicalHome, err := canonicalExistingDir(home)
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if canonical == canonicalHome {
		return "", fmt.Errorf("home directory itself is not allowed")
	}
	// AD-27 explicitly preserves the normal case: projects anywhere below
	// home remain valid, even on systems where the account home itself lives
	// below a conventionally named system tree such as /var.
	if pathAtOrUnder(canonicalHome, canonical) {
		return canonical, nil
	}

	for _, systemDir := range []string{"/etc", "/usr", "/var", "/System"} {
		canonicalSystemDir, err := canonicalPath(systemDir)
		if err != nil {
			return "", fmt.Errorf("resolve system directory %q: %w", systemDir, err)
		}
		if pathAtOrUnder(canonicalSystemDir, canonical) {
			return "", fmt.Errorf("system directory %q is not allowed", systemDir)
		}
	}

	return canonical, nil
}

func canonicalExistingDir(path string) (string, error) {
	canonical, err := canonicalPath(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("path must be an existing directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path %q is not a directory", canonical)
	}
	return canonical, nil
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return filepath.Clean(canonical), nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func pathAtOrUnder(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
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
