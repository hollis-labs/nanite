package worktree

import "fmt"

// NoopManager is a no-op worktree manager for non-git directories.
type NoopManager struct{}

// NewNoopManager returns a no-op worktree manager.
func NewNoopManager() *NoopManager { return &NoopManager{} }

func (n *NoopManager) Create(sessionID string) (string, error) {
	return "", fmt.Errorf("worktree isolation requires a git repository")
}

func (n *NoopManager) Cleanup(sessionID string) error { return nil }

func (n *NoopManager) CleanupOrphaned(activeSessionIDs map[string]bool) (int, error) {
	return 0, nil
}

func (n *NoopManager) List() []Worktree { return nil }

func (n *NoopManager) GetPath(sessionID string) (string, bool) { return "", false }
