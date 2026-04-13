package lifecycle

import (
	"os"
	"testing"

	"go.uber.org/goleak"
)

// TestMain enforces zero leaked goroutines at the end of the lifecycle test
// suite. The lifecycle.Manager is Nanite's tracked-goroutine primitive; if
// Shutdown or the G118-guarded CancelFunc ever stops draining tracked
// goroutines, goleak will fail the suite.
func TestMain(m *testing.M) {
	code := m.Run()
	if code == 0 {
		if err := goleak.Find(); err != nil {
			panic(err)
		}
	}
	os.Exit(code)
}
