package worker

import (
	"os"
	"testing"
	"time"

	"go.uber.org/goleak"
)

// TestMain drains any test-created Managers before running goleak so the
// lifecycle-tracked retention/heartbeat goroutines (which would otherwise
// sleep for 30s) are deterministically cancelled.
func TestMain(m *testing.M) {
	code := m.Run()
	testManagersMu.Lock()
	mgrs := testManagers
	testManagers = nil
	testManagersMu.Unlock()
	for _, mgr := range mgrs {
		_ = mgr.Shutdown(2 * time.Second)
	}
	if code == 0 {
		if err := goleak.Find(); err != nil {
			// Surface the leak and fail.
			panic(err)
		}
	}
	os.Exit(code)
}
