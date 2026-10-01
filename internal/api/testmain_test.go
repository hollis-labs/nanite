package api

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/testhome"
)

// TestMain provides a package-wide safety net for the few API tests that build
// a service.Container without going through newTestAPI. Per-test helpers still
// pin and assert their own t.TempDir paths; this prevents any direct container
// construction in this package from falling back to the operator's Tesseract.
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "nanite-api-tesseract-test-")
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "create API test temp root: %v\n", err)
		os.Exit(1)
	}
	// A number of API paths use os.UserHomeDir as their execution or fixture
	// root. HOME and XDG_*_HOME come from testhome.Run below
	// (CW-20260930-0208), which creates the isolated home and refuses to run
	// if nanite's layout still resolves under the real one.
	for env, dir := range map[string]string{
		"TESSERACT_DB_PATH":   filepath.Join(root, "tesseract", "main.db"),
		"TESSERACT_WORKSPACE": "api-package-test",
	} {
		if err := os.Setenv(env, dir); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "set %s for API tests: %v\n", env, err)
			os.Exit(1)
		}
	}
	code := testhome.Run(m)
	_ = os.RemoveAll(root)
	os.Exit(code)
}
