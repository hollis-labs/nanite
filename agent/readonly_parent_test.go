package agent_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/internal/provider/fake"
)

// An agent folder whose parent cannot be written still opens: the publication
// lock is skipped, since nothing can be published into that parent either.
func TestOpenWithReadOnlyParent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	parent := t.TempDir()
	dir := filepath.Join(parent, "agent")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeAgent(t, dir)
	if err := os.Chmod(parent, 0o500); err != nil { // #nosec G302 -- the test makes a directory read-only on purpose.
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) }) // #nosec G302 -- restore the temp dir so it can be removed.
	a, err := agent.Open(t.Context(), agent.Options{Dir: dir, GlobalSkillsDir: "-", Provider: fake.New()})
	if err != nil {
		t.Fatalf("Open with a read-only parent: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, ".nanite-publish-agent.lock")); !os.IsNotExist(err) {
		t.Fatalf("publication lock created in a read-only parent: %v", err)
	}
}
