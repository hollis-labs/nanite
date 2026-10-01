package workflowhost

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/testhome"
)

// TestMain keeps this package's tests out of the real home and XDG
// directories (CW-20260930-0208; see internal/testhome).
func TestMain(m *testing.M) { testhome.Main(m) }
