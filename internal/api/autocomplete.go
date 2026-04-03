package api

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// fileResult is a single autocomplete result for file references.
type fileResult struct {
	Path    string `json:"path"`     // relative path from root
	Name    string `json:"name"`     // basename
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"` // RFC3339
}

// maxFileResults caps the number of results returned.
const maxFileResults = 20

// maxWalkDepth prevents walking excessively deep trees.
const maxWalkDepth = 8

// gitignoreNames are directories always skipped during walks.
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	".next":        true,
	"dist":         true,
	"build":        true,
	"__pycache__":  true,
	".cache":       true,
	".idea":        true,
	".vscode":      true,
	"target":       true, // Rust, Java
}

// handleAutocompleteFiles returns files matching a query within the session's
// workspace/project directory. Query params: q (search string), session_id.
func (a *API) handleAutocompleteFiles(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(r.URL.Query().Get("q"))
	sessionID := r.URL.Query().Get("session_id")

	// Resolve the root directory to walk.
	root := resolveRoot(a, sessionID)
	if root == "" {
		a.jsonResp(w, http.StatusOK, []fileResult{})
		return
	}

	// Verify root exists.
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		a.jsonResp(w, http.StatusOK, []fileResult{})
		return
	}

	var results []fileResult

	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}

		// Get relative path.
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil || rel == "." {
			return nil
		}

		// Enforce depth limit.
		if strings.Count(rel, string(os.PathSeparator)) >= maxWalkDepth {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip known noise dirs but allow hidden files/dirs (e.g. .agentrc, .claude).
		base := info.Name()
		if info.IsDir() && skipDirs[base] {
			return filepath.SkipDir
		}

		// Match against query (fuzzy: check if all query chars appear in order).
		if q != "" && !fuzzyMatch(strings.ToLower(rel), q) {
			return nil
		}

		results = append(results, fileResult{
			Path:    rel,
			Name:    base,
			IsDir:   info.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime().Format("2006-01-02T15:04:05Z"),
		})

		// Early exit if we have enough candidates (we'll sort and trim later).
		if len(results) > maxFileResults*5 {
			return filepath.SkipAll
		}

		return nil
	})

	// Sort: exact basename matches first, then by path length (shorter = more relevant).
	sort.Slice(results, func(i, j int) bool {
		iExact := strings.EqualFold(results[i].Name, q)
		jExact := strings.EqualFold(results[j].Name, q)
		if iExact != jExact {
			return iExact
		}
		iStarts := strings.HasPrefix(strings.ToLower(results[i].Name), q)
		jStarts := strings.HasPrefix(strings.ToLower(results[j].Name), q)
		if iStarts != jStarts {
			return iStarts
		}
		return len(results[i].Path) < len(results[j].Path)
	})

	if len(results) > maxFileResults {
		results = results[:maxFileResults]
	}

	a.jsonResp(w, http.StatusOK, results)
}

// resolveRoot determines the filesystem root for file autocomplete.
// Priority: session's project repo_path → cwd.
func resolveRoot(a *API, sessionID string) string {
	if sessionID != "" {
		// Look up session → workspace → projects with repo_path.
		session, err := a.Services.Store.GetSession(sessionID)
		if err == nil && session != nil {
			// If session has a project_id, use that project's repo_path.
			if session.ProjectID != "" {
				projects, err := a.Services.Store.ListProjects(session.WorkspaceID)
				if err == nil {
					for _, p := range projects {
						if p.ID == session.ProjectID && p.RepoPath != "" {
							return p.RepoPath
						}
					}
				}
			}
			// Otherwise, try the first project in the workspace with a repo_path.
			if session.WorkspaceID != "" {
				projects, err := a.Services.Store.ListProjects(session.WorkspaceID)
				if err == nil {
					for _, p := range projects {
						if p.RepoPath != "" {
							return p.RepoPath
						}
					}
				}
			}
		}
	}

	// Fallback: cwd (where conduit was launched).
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}

// fuzzyMatch checks if all characters in pattern appear in str in order.
func fuzzyMatch(str, pattern string) bool {
	pi := 0
	for si := 0; si < len(str) && pi < len(pattern); si++ {
		if str[si] == pattern[pi] {
			pi++
		}
	}
	return pi == len(pattern)
}
