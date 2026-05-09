package mcp

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/envelope"
	"go.uber.org/goleak"
)

// TestMain runs goleak.VerifyTestMain to surface goroutine leaks from tests in
// this package. Also wires the shared go-envelopes Registry so tests that
// exercise envelope.ValidateData / DefaultRenderTarget find compiled
// schemas — pre-Cap-5 these came from a local //go:embed FS that has
// since been removed.
func TestMain(m *testing.M) {
	envelope.SetupForTesting()
	goleak.VerifyTestMain(m,
		// groupKillBackstop is spawned by sandbox.setProcessGroupKill on every
		// context cancellation. It intentionally outlives the cancelled command
		// by execWaitDelay+execGroupKillGrace (≈2.25s) to SIGKILL any grandchild
		// processes that survive Go's WaitDelay escalation. Not a real leak.
		goleak.IgnoreAnyFunction("github.com/hollis-labs/nanite/internal/sandbox.groupKillBackstop"),
	)
}
