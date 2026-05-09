package envelope

import (
	"os"
	"testing"
)

// TestMain wires a real go-envelopes Registry into the package-level
// SetEnvelopeRegistry seam before any tests run. Pre-Cap-5 the validator
// loaded schemas from a local //go:embed FS that has since been deleted;
// post-migration the lib is the only source.
func TestMain(m *testing.M) {
	SetupForTesting()
	os.Exit(m.Run())
}
