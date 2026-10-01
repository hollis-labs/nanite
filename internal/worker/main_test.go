package worker

import (
	"os"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/hollis-labs/nanite/internal/testhome"
)

// TestMain drains any test-created Managers before running goleak so the
// lifecycle-tracked retention/heartbeat goroutines (which would otherwise
// sleep for 30s) are deterministically canceled.
func TestMain(m *testing.M) {
	// CW-20260930-0208: keep the tests out of the real home and XDG dirs.
	code := testhome.Run(m)
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
