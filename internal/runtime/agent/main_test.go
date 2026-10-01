package agent

import (
	"fmt"
	"os"
	"testing"
)

// TestMain points CODEX_HOME at an empty temp dir for the whole package.
// Every codex boot-dir plant links auth.json to the host's codex login at
// $CODEX_HOME (or ~/.codex) (CW-20261001-0027), so without this a test
// that sets up a codex layout would link the developer's real credentials
// into its boot dir. A test that needs a host login sets its own
// CODEX_HOME with t.Setenv.
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
