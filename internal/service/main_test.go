package service

import (
	"os"
	"testing"

	"github.com/hollis-labs/nanite/internal/envelope"
)

// TestMain wires the shared go-envelopes Registry so tests that exercise
// envelope.ValidateData (e.g. tool-repair / tool-recover paths) find
// compiled schemas. Pre-Cap-5 the validator pulled schemas from a local
// //go:embed FS that has since been removed; the lib's manifest is now
// the single source of truth.
func TestMain(m *testing.M) {
	envelope.SetupForTesting()
	os.Exit(m.Run())
}
