package recover

import (
	"os"
	"testing"

	"github.com/hollis-labs/nanite/internal/envelope"
)

// TestMain wires the shared go-envelopes Registry so tests that classify
// envelope ValidationErrors find compiled schemas. Pre-Cap-5 the
// underlying loadSchema fell back to a local //go:embed FS that has
// since been removed; the lib's manifest is now the single source.
func TestMain(m *testing.M) {
	envelope.SetupForTesting()
	os.Exit(m.Run())
}
