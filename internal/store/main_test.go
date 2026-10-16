package store_test

import (
	"os"
	"testing"

	"github.com/hollis-labs/nanite/internal/storetest/migratedtemplate"
)

// TestMain removes the migrated-database templates the tests share once every
// test has run; they are kept for the whole binary so each is built once.
func TestMain(m *testing.M) {
	code := m.Run()
	migratedtemplate.Cleanup()
	os.Exit(code)
}
