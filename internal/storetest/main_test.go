package storetest

import (
	"os"
	"testing"

	"github.com/hollis-labs/nanite/internal/storetest/migratedtemplate"
)

// TestMain removes the migrated template this package's tests share.
func TestMain(m *testing.M) {
	code := m.Run()
	migratedtemplate.Cleanup()
	os.Exit(code)
}
