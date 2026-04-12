// Regression test for BLG-20260412-009 — adapter-claude atomic writes.
package adapterclaude

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestWriteFile_NoPartialFileOnRenameFailure invokes the package writeFile
// helper (now backed by fsutil.AtomicWriteFile) against a target inside a
// read-only directory. The write must fail, and the target must not exist.
func TestWriteFile_NoPartialFileOnRenameFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only: relies on POSIX directory permission bits")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write permissions")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if err := writeFile(dir, "CLAUDE.md", "body"); err == nil {
		t.Fatal("expected error writing into read-only dir")
	}

	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatalf("target present after failed write: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		t.Errorf("stray entry after failed write: %q", e.Name())
	}
}
