package worker

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs goleak.VerifyTestMain to surface goroutine leaks from tests in
// this package. New worker code going forward should rely on
// golang.org/x/sync/errgroup for goroutine fan-out so leaks are attributable.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
