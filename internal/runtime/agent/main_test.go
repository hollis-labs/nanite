package agent

import (
	"fmt"
	"os"
	"testing"
)

// TestMain points CODEX_HOME at an empty temp dir for the whole package.
// Every codex boot-dir plant copies the host's codex auth.json from
// $CODEX_HOME (or ~/.codex) into the boot dir (CW-20261001-0021), so
// without this a test that sets up a codex layout would copy the
// developer's real credentials. A test that needs a source auth.json sets
// its own CODEX_HOME with t.Setenv.
func TestMain(m *testing.M) {
	codexHome, err := os.MkdirTemp("", "nanite-agent-test-codex-home-")
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "create codex home for agent tests: %v\n", err)
		os.Exit(1)
	}
	if err := os.Setenv("CODEX_HOME", codexHome); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "set CODEX_HOME for agent tests: %v\n", err)
		_ = os.RemoveAll(codexHome)
		os.Exit(1)
	}

	code := m.Run()
	_ = os.RemoveAll(codexHome)
	os.Exit(code)
}
