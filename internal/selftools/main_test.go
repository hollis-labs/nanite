package selftools

import (
	"fmt"
	"os"
	"testing"

	"github.com/hollis-labs/nanite/internal/envelope"
	"go.uber.org/goleak"

	"github.com/hollis-labs/nanite/internal/testhome"
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
	// CW-20260930-0208: keep the tests out of the real home and XDG dirs,
	// then surface goroutine leaks as goleak.VerifyTestMain did.
	code := testhome.Run(m)
	if code == 0 {
		if err := goleak.Find(); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "goleak: %v\n", err)
			code = 1
		}
	}
	os.Exit(code)
}
