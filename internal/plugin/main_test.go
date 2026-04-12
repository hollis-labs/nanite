package plugin

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs goleak.VerifyTestMain to surface goroutine leaks from tests in
// this package.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
