package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A TMPDIR reached through a symlink must not leak into boot roots: the
// Harness workspace authority refuses non-physical paths.
func TestMkdirTempCanonicalResolvesSymlinkedTmpdir(t *testing.T) {
	physical, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(physical, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", link)

	dir, err := mkdirTempCanonical("nanite-canonical-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if filepath.Dir(dir) != physical || !strings.HasPrefix(filepath.Base(dir), "nanite-canonical-") {
		t.Fatalf("dir %q is not under physical %q", dir, physical)
	}
	if resolved, err := filepath.EvalSymlinks(dir); err != nil || resolved != dir {
		t.Fatalf("dir not canonical: %q -> %q (%v)", dir, resolved, err)
	}
}
