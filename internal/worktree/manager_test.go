package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initTestRepo creates a temporary git repo for testing.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s: %v", args, out, err)
		}
	}

	// Need at least one commit for worktrees to work.
	f := filepath.Join(dir, "README.md")
	os.WriteFile(f, []byte("test"), 0o644)
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = dir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s: %v", out, err)
	}

	return dir
}

func TestCreateAndCleanup(t *testing.T) {
	repoDir := initTestRepo(t)

	// Override working directory for git rev-parse detection.
	origDir, _ := os.Getwd()
	os.Chdir(repoDir)
	defer os.Chdir(origDir)

	baseDir := filepath.Join(repoDir, ".nanite", "worktrees")
	mgr, err := NewManager(baseDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Create worktree.
	path, err := mgr.Create("test-session-123")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify directory exists.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("worktree directory should exist")
	}

	// Verify it's listed.
	list := mgr.List()
	if len(list) != 1 {
		t.Fatalf("List: got %d, want 1", len(list))
	}
	if list[0].SessionID != "test-session-123" {
		t.Errorf("SessionID = %q, want test-session-123", list[0].SessionID)
	}

	// GetPath should return it.
	gotPath, ok := mgr.GetPath("test-session-123")
	if !ok || gotPath != path {
		t.Errorf("GetPath = (%q, %v), want (%q, true)", gotPath, ok, path)
	}

	// Cleanup.
	if err := mgr.Cleanup("test-session-123"); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	// Verify removed.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("worktree directory should be removed after cleanup")
	}

	list = mgr.List()
	if len(list) != 0 {
		t.Errorf("List after cleanup: got %d, want 0", len(list))
	}
}

func TestCreateIdempotent(t *testing.T) {
	repoDir := initTestRepo(t)
	origDir, _ := os.Getwd()
	os.Chdir(repoDir)
	defer os.Chdir(origDir)

	mgr, _ := NewManager(filepath.Join(repoDir, ".nanite", "worktrees"))

	path1, err := mgr.Create("sess-1")
	if err != nil {
		t.Fatalf("Create 1: %v", err)
	}

	path2, err := mgr.Create("sess-1")
	if err != nil {
		t.Fatalf("Create 2: %v", err)
	}

	if path1 != path2 {
		t.Errorf("idempotent create: paths differ: %q vs %q", path1, path2)
	}

	// Cleanup.
	mgr.Cleanup("sess-1")
}

func TestCleanupOrphaned(t *testing.T) {
	repoDir := initTestRepo(t)
	origDir, _ := os.Getwd()
	os.Chdir(repoDir)
	defer os.Chdir(origDir)

	mgr, _ := NewManager(filepath.Join(repoDir, ".nanite", "worktrees"))

	// Create two worktrees.
	mgr.Create("active-session")
	mgr.Create("orphan-session")

	// Only active-session is in the active set.
	active := map[string]bool{"active-session": true}
	cleaned, err := mgr.CleanupOrphaned(active)
	if err != nil {
		t.Fatalf("CleanupOrphaned: %v", err)
	}
	if cleaned != 1 {
		t.Errorf("cleaned = %d, want 1", cleaned)
	}

	list := mgr.List()
	if len(list) != 1 {
		t.Fatalf("List: got %d, want 1", len(list))
	}
	if list[0].SessionID != "active-session" {
		t.Errorf("remaining session = %q, want active-session", list[0].SessionID)
	}

	// Cleanup remaining.
	mgr.Cleanup("active-session")
}

func TestCleanupNonexistent(t *testing.T) {
	repoDir := initTestRepo(t)
	origDir, _ := os.Getwd()
	os.Chdir(repoDir)
	defer os.Chdir(origDir)

	mgr, _ := NewManager(filepath.Join(repoDir, ".nanite", "worktrees"))

	// Cleanup a session that was never created.
	if err := mgr.Cleanup("nonexistent"); err != nil {
		t.Errorf("Cleanup nonexistent: %v", err)
	}
}

func TestNoopManager(t *testing.T) {
	mgr := NewNoopManager()

	_, err := mgr.Create("sess-1")
	if err == nil {
		t.Error("NoopManager.Create should return error")
	}

	if err := mgr.Cleanup("sess-1"); err != nil {
		t.Errorf("Cleanup: %v", err)
	}

	cleaned, err := mgr.CleanupOrphaned(nil)
	if err != nil || cleaned != 0 {
		t.Errorf("CleanupOrphaned: %d, %v", cleaned, err)
	}

	if list := mgr.List(); len(list) != 0 {
		t.Errorf("List: got %d, want 0", len(list))
	}

	if _, ok := mgr.GetPath("sess-1"); ok {
		t.Error("GetPath should return false")
	}
}
