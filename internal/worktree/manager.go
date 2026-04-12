package worktree

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Worktree represents an active git worktree tied to a worker session.
type Worktree struct {
	SessionID string    `json:"session_id"`
	Path      string    `json:"path"`
	Branch    string    `json:"branch"`
	CreatedAt time.Time `json:"created_at"`
}

// Manager manages git worktree lifecycle for worker session isolation.
type Manager interface {
	// Create creates a new git worktree for the given session.
	// Returns the absolute path to the worktree directory.
	Create(sessionID string) (worktreePath string, err error)

	// Cleanup removes the worktree for the given session.
	Cleanup(sessionID string) error

	// CleanupOrphaned removes worktrees whose sessions no longer exist.
	// Returns the number of orphaned worktrees cleaned up.
	CleanupOrphaned(activeSessionIDs map[string]bool) (int, error)

	// List returns all active worktrees managed by this manager.
	List() []Worktree

	// GetPath returns the worktree path for a session, if one exists.
	GetPath(sessionID string) (string, bool)
}

// gitManager implements Manager using git worktree commands.
type gitManager struct {
	baseDir  string // directory under which worktrees are created
	repoRoot string // the main repo root (detected at init)
	mu       sync.Mutex
	active   map[string]*Worktree
}

// NewManager creates a worktree manager. baseDir is the directory under which
// worktrees will be created (e.g., ".nanite/worktrees"). The current directory
// must be inside a git repository.
func NewManager(baseDir string) (Manager, error) {
	// Detect git repo root.
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}
	repoRoot := strings.TrimSpace(string(out))

	// Ensure base directory exists.
	absBase := baseDir
	if !filepath.IsAbs(absBase) {
		absBase = filepath.Join(repoRoot, absBase)
	}
	if err := os.MkdirAll(absBase, 0o755); err != nil {
		return nil, fmt.Errorf("create worktree base dir: %w", err)
	}

	return &gitManager{
		baseDir:  absBase,
		repoRoot: repoRoot,
		active:   make(map[string]*Worktree),
	}, nil
}

func (m *gitManager) Create(sessionID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if already exists.
	if wt, ok := m.active[sessionID]; ok {
		return wt.Path, nil
	}

	branch := "worker-" + sessionID
	wtPath := filepath.Join(m.baseDir, sessionID)

	// Create the worktree with a new branch.
	cmd := exec.Command("git", "worktree", "add", wtPath, "-b", branch)
	cmd.Dir = m.repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git worktree add: %s: %w", strings.TrimSpace(string(out)), err)
	}

	wt := &Worktree{
		SessionID: sessionID,
		Path:      wtPath,
		Branch:    branch,
		CreatedAt: time.Now().UTC(),
	}
	m.active[sessionID] = wt
	slog.Info("worktree: created", "session_id", sessionID, "path", wtPath, "branch", branch)
	return wtPath, nil
}

func (m *gitManager) Cleanup(sessionID string) error {
	m.mu.Lock()
	wt, ok := m.active[sessionID]
	if ok {
		delete(m.active, sessionID)
	}
	m.mu.Unlock()

	if !ok {
		return nil // nothing to clean up
	}

	// Remove the worktree.
	cmd := exec.Command("git", "worktree", "remove", wt.Path, "--force")
	cmd.Dir = m.repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		slog.Warn("worktree: remove failed", "path", wt.Path, "output", strings.TrimSpace(string(out)), "err", err)
		// Try manual cleanup as fallback.
		os.RemoveAll(wt.Path)
	}

	// Delete the branch.
	cmd = exec.Command("git", "branch", "-D", wt.Branch)
	cmd.Dir = m.repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		slog.Warn("worktree: delete branch failed", "branch", wt.Branch, "output", strings.TrimSpace(string(out)), "err", err)
	}

	// Prune stale worktree references.
	exec.Command("git", "worktree", "prune").Run()

	slog.Info("worktree: cleaned up", "session_id", sessionID)
	return nil
}

func (m *gitManager) CleanupOrphaned(activeSessionIDs map[string]bool) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check filesystem for worktree directories.
	entries, err := os.ReadDir(m.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read worktree dir: %w", err)
	}

	cleaned := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sessionID := e.Name()
		if activeSessionIDs != nil && activeSessionIDs[sessionID] {
			continue // session still active
		}

		wtPath := filepath.Join(m.baseDir, sessionID)
		short := sessionID
		if len(short) > 8 {
			short = short[:8]
		}
		branch := "worker-" + short

		// Remove worktree.
		cmd := exec.Command("git", "worktree", "remove", wtPath, "--force")
		cmd.Dir = m.repoRoot
		cmd.Run() // best-effort

		// Remove directory if git didn't.
		os.RemoveAll(wtPath)

		// Delete branch.
		cmd = exec.Command("git", "branch", "-D", branch)
		cmd.Dir = m.repoRoot
		cmd.Run() // best-effort

		delete(m.active, sessionID)
		cleaned++
	}

	if cleaned > 0 {
		exec.Command("git", "worktree", "prune").Run()
	}

	return cleaned, nil
}

func (m *gitManager) List() []Worktree {
	m.mu.Lock()
	defer m.mu.Unlock()

	list := make([]Worktree, 0, len(m.active))
	for _, wt := range m.active {
		list = append(list, *wt)
	}
	return list
}

func (m *gitManager) GetPath(sessionID string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	wt, ok := m.active[sessionID]
	if !ok {
		return "", false
	}
	return wt.Path, true
}
