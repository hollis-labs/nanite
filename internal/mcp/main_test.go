package mcp

import (
	"fmt"
	"os"
	"testing"

	"github.com/hollis-labs/nanite/internal/envelope"
	"go.uber.org/goleak"

	"github.com/hollis-labs/nanite/internal/testhome"
)

// TestMain runs goleak.VerifyTestMain to surface goroutine leaks from tests in
// this package. Also wires the shared go-envelopes Registry so tests that
// exercise envelope.ValidateData / DefaultRenderTarget find compiled
// schemas — pre-Cap-5 these came from a local //go:embed FS that has
// since been removed.
//
// It first checks runAsFixtureServerEnv (stdio_fixture_test.go): when set,
// this test binary re-execs itself as a real MCP stdio server instead of
// running any tests at all -- the "fork and exec the test binary itself"
// trick the official SDK's own cmd_test.go uses, so stdio-backed tests
// (restart_stdio_test.go) have a real, spec-compliant server to dial
// without a separate compiled fixture or a hand-rolled protocol
// simulation that can drift from what the real SDK client actually sends
// (see stdio_fixture_test.go's doc comment for the concrete way a
// hand-rolled one did).
func TestMain(m *testing.M) {
	if os.Getenv(runAsFixtureServerEnv) != "" {
		runFixtureServer()
		return
	}

	envelope.SetupForTesting()
	// CW-20260930-0208: keep the tests out of the real home (dev_bash's
	// sandbox dir is ~/.nanite/sandboxes) and XDG dirs, then surface goroutine
	// leaks as goleak.VerifyTestMain did.
	code := testhome.Run(m)
	if code == 0 {
		if err := goleak.Find(
			// groupKillBackstop is spawned by sandbox.setProcessGroupKill on every
			// context cancellation. It intentionally outlives the canceled command
			// by execWaitDelay+execGroupKillGrace (≈2.25s) to SIGKILL any grandchild
			// processes that survive Go's WaitDelay escalation. Not a real leak.
			goleak.IgnoreAnyFunction("github.com/hollis-labs/nanite/internal/sandbox.groupKillBackstop"),
		); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "goleak: %v\n", err)
			code = 1
		}
	}
	os.Exit(code)
}
