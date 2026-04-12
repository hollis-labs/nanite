// Regression test for BLG-20260412-009 — adapter-opencode atomic writes.
package adapteropencode

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestPopulateSandbox_NoPartialFileOnWriteFailure targets a read-only
// sandbox directory. AtomicWriteFile's temp-create step must fail and no
// OPENCODE.md must appear.
func TestPopulateSandbox_NoPartialFileOnWriteFailure(t *testing.T) {
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

	a := New().Adapter()
	err := a.PopulateSandbox(dir, store.AgentProfile{Name: "x", Slug: "x"}, agent.SandboxContext{})
	if err == nil {
		t.Fatal("expected write error against read-only sandbox dir")
	}

	if _, err := os.Stat(filepath.Join(dir, "OPENCODE.md")); !os.IsNotExist(err) {
		t.Fatalf("OPENCODE.md present after failed write: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		t.Errorf("stray entry after failed write: %q", e.Name())
	}
}
