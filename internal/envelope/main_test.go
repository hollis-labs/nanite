package envelope

import (
	"os"
	"testing"

	"github.com/hollis-labs/nanite/internal/testhome"
)

// TestMain wires a real go-envelopes Registry into the package-level
// SetEnvelopeRegistry seam before any tests run. Pre-Cap-5 the validator
// loaded schemas from a local //go:embed FS that has since been deleted;
// post-migration the lib is the only source.
func TestMain(m *testing.M) {
	SetupForTesting()
	// CW-20260930-0208: keep the tests out of the real home and XDG dirs.
	os.Exit(testhome.Run(m))
}
