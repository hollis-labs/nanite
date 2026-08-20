package selftools

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/envelope"
	"go.uber.org/goleak"
)

// TestMain runs goleak.VerifyTestMain to surface goroutine leaks from tests
// in this package. Also wires the shared go-envelopes Registry so tests that
// exercise envelope.ValidateData / DefaultRenderTarget (e.g. callShowCard's
// schema validation) find compiled schemas.
//
// Mirrors internal/mcp/main_test.go — a necessary duplicate, not a design
// choice: Go test binaries are per-package, and internal/selftools split off
// from internal/mcp (TASKS/harness-reactive-self-tools/01) into its own
// package/test binary, so it needs its own TestMain doing the identical
// setup internal/mcp's TestMain already did for these same tests before the
// move.
func TestMain(m *testing.M) {
	envelope.SetupForTesting()
	goleak.VerifyTestMain(m)
}
