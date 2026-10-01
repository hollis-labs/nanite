package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
)

// SessionGetter reads one session.
type SessionGetter interface {
	GetSession(ctx context.Context, id string) (*store.Session, error)
}

// ProjectService owns project rows and the two rules transports apply to
// them: which repository paths a project may point at (AD-27,
// ValidateProjectRepoPath), and which directory file autocomplete may walk
// for a session (AutocompleteRoot). Reads and writes are otherwise
// pass-throughs; store errors come back unwrapped.
type ProjectService struct {
	store    ProjectStore
	sessions SessionGetter
}

// NewProjectService builds the service. sessions backs AutocompleteRoot and
// may be nil, in which case no session's project is consulted.
func NewProjectService(st ProjectStore, sessions SessionGetter) *ProjectService {
	return &ProjectService{store: st, sessions: sessions}
}

// ErrProjectNotFound reports a project that could not be read.
var ErrProjectNotFound = errors.New("project not found")

// ErrProjectNoRepoPath reports a project with no repository attached.
var ErrProjectNoRepoPath = errors.New("project has no repo_path")

// ProjectValidationError reports a project field the rules reject. Its
// message is meant for the caller.
type ProjectValidationError struct {
	Msg string
}

func (e *ProjectValidationError) Error() string { return e.Msg }

// ProjectPatch is an update to a project. A nil field keeps the stored value.
type ProjectPatch struct {
	Name        *string
	Description *string
	RepoPath    *string
	Settings    *string
	SortOrder   *int
}

// List returns every project.
func (s *ProjectService) List(ctx context.Context) ([]store.Project, error) {
	return s.store.ListProjects(ctx)
}

// Get returns a project. Any read failure, not only a missing row, is an
// error.
func (s *ProjectService) Get(ctx context.Context, id string) (*store.Project, error) {
	return s.store.GetProject(ctx, id)
}

// Create validates p's repository path (canonicalizing it in place) and
// inserts the project. A rejected path is a *ProjectValidationError.
func (s *ProjectService) Create(ctx context.Context, p *store.Project) error {
	repoPath, err := ValidateProjectRepoPath(p.RepoPath)
	if err != nil {
		return &ProjectValidationError{Msg: "invalid repo_path: " + err.Error()}
	}
	p.RepoPath = repoPath
	return s.store.CreateProject(ctx, p)
}

// Update applies patch onto the stored project existing and writes it. The
// repository path is validated only when the patch sets it, so an existing
// row is never re-judged by a later rule until its path is edited. A
// rejected path is a *ProjectValidationError and nothing is written.
func (s *ProjectService) Update(ctx context.Context, existing *store.Project, patch ProjectPatch) error {
	if patch.Name != nil {
		existing.Name = *patch.Name
	}
	if patch.Description != nil {
		existing.Description = *patch.Description
	}
	if patch.RepoPath != nil {
		repoPath, err := ValidateProjectRepoPath(*patch.RepoPath)
		if err != nil {
			return &ProjectValidationError{Msg: "invalid repo_path: " + err.Error()}
		}
		existing.RepoPath = repoPath
	}
	if patch.Settings != nil {
		existing.Settings = *patch.Settings
	}
	if patch.SortOrder != nil {
		existing.SortOrder = *patch.SortOrder
	}
	return s.store.UpdateProject(ctx, existing)
}

// Delete removes a project. A project that cannot be read is
// ErrProjectNotFound and nothing is deleted.
func (s *ProjectService) Delete(ctx context.Context, id string) error {
	if _, err := s.store.GetProject(ctx, id); err != nil {
		return ErrProjectNotFound
	}
	return s.store.DeleteProject(ctx, id)
}

// WorkRoot returns the directory a CLI agent in one of project id's
// sessions works in: the project's repo_path, which must still name an
// existing directory (CW-20261001-0020). It is ErrProjectNotFound for a
// project that cannot be read and ErrProjectNoRepoPath for one with no
// repository attached; any other error is a repo_path that no longer
// resolves. The harness v1 session create calls it so a project-scoped
// session fails up front instead of booting an agent that cannot see its
// project.
func (s *ProjectService) WorkRoot(ctx context.Context, id string) (string, error) {
	return projectWorkRoot(ctx, s.store, id)
}

// projectWorkRoot is WorkRoot over any project reader; the chat service
// resolves the same root at boot through its own store.
func projectWorkRoot(ctx context.Context, st interface {
	GetProject(ctx context.Context, id string) (*store.Project, error)
}, id string) (string, error) {
	p, err := st.GetProject(ctx, id)
	if err != nil || p == nil {
		return "", fmt.Errorf("%w: %q", ErrProjectNotFound, id)
	}
	if strings.TrimSpace(p.RepoPath) == "" {
		return "", fmt.Errorf("%w: project %q", ErrProjectNoRepoPath, id)
	}
	root, err := canonicalExistingDir(p.RepoPath)
	if err != nil {
		return "", fmt.Errorf("project %q repo_path %q: %w", id, p.RepoPath, err)
	}
	return root, nil
}

// AutocompleteRoot returns the directory file autocomplete walks for a
// session. When the session exists: its project's repo_path if set, else
// the first project (in list order) that has one. Otherwise, and when no
// session id is given, the process working directory; "" if even that
// cannot be read.
//
// The lookups run under the caller's ctx. Once a request is canceled they
// fail and the working directory is used, which only a client that has
// already gone could notice.
func (s *ProjectService) AutocompleteRoot(ctx context.Context, sessionID string) string {
	if sessionID != "" && s.sessions != nil {
		session, err := s.sessions.GetSession(ctx, sessionID)
		if err == nil && session != nil {
			if session.ProjectID != "" {
				if p, err := s.store.GetProject(ctx, session.ProjectID); err == nil && p != nil && p.RepoPath != "" {
					return p.RepoPath
				}
			}
			projects, err := s.store.ListProjects(ctx)
			if err == nil {
				for _, p := range projects {
					if p.RepoPath != "" {
						return p.RepoPath
					}
				}
			}
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}

// ValidateProjectRepoPath applies AD-27 at the write boundary. Empty means a
// project has no repository attached. Non-empty paths are canonicalized so a
// symlink cannot disguise a forbidden root, must already name a directory,
// and may not expose a whole filesystem, the user's whole home, or a named
// system tree to metadata enumeration through autocomplete.
func ValidateProjectRepoPath(repoPath string) (string, error) {
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
