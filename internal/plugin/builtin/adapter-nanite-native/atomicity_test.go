// Regression test for BLG-20260412-009 — adapter-nanite-native atomic writes.
package nanitenative

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestPopulateSandbox_NoPartialFileOnWriteFailure pre-creates the .nanite
// subdirectory inside the sandbox and chmods it read-only. PopulateSandbox's
// MkdirAll is a no-op on the existing dir; AtomicWriteFile must then fail
// and leave no config.yaml (partial or otherwise).
func TestPopulateSandbox_NoPartialFileOnWriteFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only: relies on POSIX directory permission bits")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write permissions")
	}
	sandbox := t.TempDir()
	naniteDir := filepath.Join(sandbox, ".nanite")
	if err := os.Mkdir(naniteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(naniteDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(naniteDir, 0o755) })

	a := New().Adapter()
	err := a.PopulateSandbox(sandbox, store.AgentProfile{Name: "x", Slug: "x"}, agent.SandboxContext{})
	if err == nil {
		t.Fatal("expected write error with read-only .nanite dir")
	}

	if _, err := os.Stat(filepath.Join(naniteDir, "config.yaml")); !os.IsNotExist(err) {
		t.Fatalf("config.yaml present after failed write: %v", err)
	}
	entries, _ := os.ReadDir(naniteDir)
	for _, e := range entries {
		t.Errorf("stray entry after failed write: %q", e.Name())
	}
}
