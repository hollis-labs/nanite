//go:build !(darwin || linux)

package sandbox

import (
	"log/slog"
	"os/exec"
	"sync"
)

var osWarnOnce sync.Once

// applyOSSandbox is a no-op on non-darwin platforms. It logs a warning once
// and falls through to Tier 1 (convention-level) sandbox.
func applyOSSandbox(cmd *exec.Cmd, sandboxDir string, networkAllow []string) (cleanup func(), err error) {
	osWarnOnce.Do(func() {
		slog.Warn("sandbox: OS-level isolation unavailable on this platform — using Tier 1 (convention-level) sandbox only")
	})
	return func() {}, nil
}
