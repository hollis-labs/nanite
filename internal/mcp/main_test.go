package mcp

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs goleak.VerifyTestMain to surface goroutine leaks from tests in
// this package.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		// groupKillBackstop is spawned by sandbox.setProcessGroupKill on every
		// context cancellation. It intentionally outlives the cancelled command
		// by execWaitDelay+execGroupKillGrace (≈2.25s) to SIGKILL any grandchild
		// processes that survive Go's WaitDelay escalation. Not a real leak.
		goleak.IgnoreAnyFunction("github.com/hollis-labs/nanite/internal/sandbox.groupKillBackstop"),
	)
}
