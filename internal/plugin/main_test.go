package plugin

import (
	"fmt"
	"os"
	"testing"

	"go.uber.org/goleak"

	"github.com/hollis-labs/nanite/internal/testhome"
)

// TestMain keeps the tests out of the real home and XDG dirs
// (CW-20260930-0208: the lazy plugin state store resolves and opens the
// layout's main database), then surfaces goroutine leaks as
// goleak.VerifyTestMain did.
func TestMain(m *testing.M) {
	code := testhome.Run(m)
	if code == 0 {
		if err := goleak.Find(); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "goleak: %v\n", err)
			code = 1
		}
	}
	os.Exit(code)
}
