//go:build !(darwin || linux)

package sandbox

import (
	"log/slog"
	"os/exec"
	"sync"
)

var osWarnOnce sync.Once

// applyOSSandbox is a no-op on non-darwin/non-linux platforms. It logs a
// warning once and falls through to Tier 1 (convention-level) sandbox.
// extraWritePath is accepted for signature parity with the darwin/linux
// implementations and is intentionally ignored here — without OS sandbox
// enforcement there's no allow-write list to widen.
func applyOSSandbox(cmd *exec.Cmd, sandboxDir string, extraWritePath string, networkAllow []string) (cleanup func(), err error) {
	_ = extraWritePath
	osWarnOnce.Do(func() {
		slog.Warn("sandbox: OS-level isolation unavailable on this platform — using Tier 1 (convention-level) sandbox only")
	})
	return func() {}, nil
}
