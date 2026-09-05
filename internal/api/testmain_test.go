package api

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
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
	// root. TestMain points HOME at this isolated tree, so create the directory
	// before tests attempt to chdir or create children beneath it.
	if err := os.MkdirAll(filepath.Join(root, "home"), 0o700); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "create API test home: %v\n", err)
		os.Exit(1)
	}
	for env, dir := range map[string]string{
		"HOME":                filepath.Join(root, "home"),
		"XDG_DATA_HOME":       filepath.Join(root, "xdg", "data"),
		"XDG_STATE_HOME":      filepath.Join(root, "xdg", "state"),
		"XDG_CACHE_HOME":      filepath.Join(root, "xdg", "cache"),
		"XDG_CONFIG_HOME":     filepath.Join(root, "xdg", "config"),
		"TESSERACT_DB_PATH":   filepath.Join(root, "tesseract", "main.db"),
		"TESSERACT_WORKSPACE": "api-package-test",
	} {
		if err := os.Setenv(env, dir); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "set %s for API tests: %v\n", env, err)
			os.Exit(1)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}
