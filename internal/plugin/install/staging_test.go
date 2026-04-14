package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func newStaging(t *testing.T) (*DirStaging, string, string) {
	t.Helper()
	root := t.TempDir()
	s := &DirStaging{
		StagingRoot: filepath.Join(root, "staging"),
		PluginsRoot: filepath.Join(root, "plugins"),
	}
	return s, s.StagingRoot, s.PluginsRoot
}

func TestStaging_BeginCommit_HappyPath(t *testing.T) {
	s, stagingRoot, pluginsRoot := newStaging(t)
	dir, cleanup, err := s.Begin(context.Background(), "giphy")
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if !strings.HasPrefix(dir, stagingRoot) {
		t.Errorf("staging dir %q not under %q", dir, stagingRoot)
	}
	// Put a file in staging.
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	final, err := s.Commit(context.Background(), dir, "giphy")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if final != filepath.Join(pluginsRoot, "giphy") {
		t.Errorf("final = %q", final)
	}
	if _, err := os.Stat(filepath.Join(final, "plugin.yaml")); err != nil {
		t.Errorf("plugin.yaml missing: %v", err)
	}
	// Lock released.
	if _, err := os.Stat(filepath.Join(stagingRoot, "giphy.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("lock still exists: %v", err)
	}
	// cleanup is a no-op on success.
	cleanup()
}

func TestStaging_Begin_ConcurrentLock(t *testing.T) {
	s, _, _ := newStaging(t)
	_, cleanup1, err := s.Begin(context.Background(), "giphy")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup1()
	_, _, err = s.Begin(context.Background(), "giphy")
	if err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("err = %v; want concurrency error", err)
	}
}

func TestStaging_Cleanup_RemovesStagingAndLock(t *testing.T) {
	s, stagingRoot, _ := newStaging(t)
	dir, cleanup, err := s.Begin(context.Background(), "giphy")
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("staging dir still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stagingRoot, "giphy.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("lock still exists: %v", err)
	}
}

func TestStaging_Commit_UpdatesExisting(t *testing.T) {
	s, _, pluginsRoot := newStaging(t)
	// Pre-existing install.
	oldDir := filepath.Join(pluginsRoot, "giphy")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "OLD"), []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir, _, err := s.Begin(context.Background(), "giphy")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "NEW"), []byte("v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	final, err := s.Commit(context.Background(), dir, "giphy")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(final, "OLD")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("OLD still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(final, "NEW")); err != nil {
		t.Errorf("NEW missing: %v", err)
	}
	// No leftover .backup-* dirs.
	ents, _ := os.ReadDir(pluginsRoot)
	for _, e := range ents {
		if strings.Contains(e.Name(), ".backup-") {
			t.Errorf("leftover backup dir %q", e.Name())
		}
	}
}

func TestStaging_Commit_RejectsStagingOutsideRoot(t *testing.T) {
	s, _, _ := newStaging(t)
	other := t.TempDir()
	_, err := s.Commit(context.Background(), other, "giphy")
	if err == nil || !strings.Contains(err.Error(), "not under staging root") {
		t.Fatalf("err = %v", err)
	}
}

func TestStaging_Commit_RejectsBadPluginID(t *testing.T) {
	s, _, _ := newStaging(t)
	cases := []string{"", "Bad", "has space", "has/slash", "has..dot"}
	for _, id := range cases {
		t.Run(id, func(t *testing.T) {
			_, err := s.Commit(context.Background(), "/tmp", id)
			if err == nil {
				t.Fatalf("expected error for id %q", id)
			}
		})
	}
}

func TestStaging_Validate_MissingRoots(t *testing.T) {
	cases := []*DirStaging{
		{},
		{StagingRoot: "/tmp"},
		{PluginsRoot: "/tmp"},
	}
	for i, c := range cases {
		if _, _, err := c.Begin(context.Background(), "giphy"); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}

func TestIsCrossDeviceError(t *testing.T) {
	if !isCrossDeviceError(&os.LinkError{Err: syscall.EXDEV}) {
		t.Error("EXDEV not detected")
	}
	if isCrossDeviceError(errors.New("other")) {
		t.Error("false positive")
	}
}
