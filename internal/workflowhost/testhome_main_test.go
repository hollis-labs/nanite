package workflowhost

import (
	"os"
	"testing"

	"github.com/hollis-labs/nanite/internal/storetest/migratedtemplate"
	"github.com/hollis-labs/nanite/internal/testhome"
)

// TestMain keeps this package's tests out of the real home and XDG
// directories (CW-20260930-0208; see internal/testhome) and removes the
// migrated-database templates once every test has run.
func TestMain(m *testing.M) {
	code := testhome.Run(m)
	migratedtemplate.Cleanup()
	os.Exit(code)
}
