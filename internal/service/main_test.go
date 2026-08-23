package service

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/envelope"
)

var serviceTestRoot string

// TestMain wires the shared go-envelopes Registry and isolates every
// package-level path resolver before any service test can construct a
// Container. The test binary gets one unique root, so parallel tests share
// stable environment values while concurrent package binaries cannot collide.
func TestMain(m *testing.M) {
	envelope.SetupForTesting()

	root, err := os.MkdirTemp("", "nanite-service-test-")
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "create service test temp root: %v\n", err)
		os.Exit(1)
	}
	serviceTestRoot = root
	for env, value := range map[string]string{
		"HOME":                filepath.Join(root, "home"),
		"XDG_DATA_HOME":       filepath.Join(root, "xdg", "data"),
		"XDG_STATE_HOME":      filepath.Join(root, "xdg", "state"),
		"XDG_CACHE_HOME":      filepath.Join(root, "xdg", "cache"),
		"XDG_CONFIG_HOME":     filepath.Join(root, "xdg", "config"),
		"TESSERACT_DB_PATH":   filepath.Join(root, "tesseract", "main.db"),
		"TESSERACT_WORKSPACE": "service-package-test",
	} {
		if err := os.Setenv(env, value); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "set %s for service tests: %v\n", env, err)
			_ = os.RemoveAll(root)
			os.Exit(1)
		}
	}

	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}
