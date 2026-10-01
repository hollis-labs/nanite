package service

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/envelope"

	"github.com/hollis-labs/nanite/internal/testhome"
)

var serviceTestRoot string

// TestMain wires the shared go-envelopes Registry and isolates every
// package-level path resolver before any service test can construct a
// Container. The test binary gets one unique root, so parallel tests share
// stable environment values while concurrent package binaries cannot collide.
func TestMain(m *testing.M) {
	envelope.SetupForTesting()

	// HOME and XDG_*_HOME come from testhome (CW-20260930-0208), which also
	// refuses to run if nanite's layout still resolves under the real home.
	// Its temp tree is this package's test root, so the container tests'
	// "everything lands under serviceTestRoot" checks cover HOME and XDG too.
	cleanup, err := testhome.Isolate()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "testhome: %v\n", err)
		os.Exit(1)
	}
	root := testhome.Root()
	serviceTestRoot = root
	for env, value := range map[string]string{
		"TESSERACT_DB_PATH":   filepath.Join(root, "tesseract", "main.db"),
		"TESSERACT_WORKSPACE": "service-package-test",
		// ACP tests here drive fake adapter factories, no ACP CLI process,
		// so they opt in to the runtime layer's ACP launch gate; the gate's
		// own tests turn it back off with t.Setenv.
		"NANITE_ALLOW_ACP_RUNTIMES": "1",
	} {
		if err := os.Setenv(env, value); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "set %s for service tests: %v\n", env, err)
			cleanup()
			os.Exit(1)
		}
	}

	code := m.Run()
	cleanup()
	os.Exit(code)
}
