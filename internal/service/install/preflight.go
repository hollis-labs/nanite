package install

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
)

// lookPath is the hook used by checkBwrap to locate bwrap on PATH. Tests
// override this to simulate bubblewrap-missing environments without
// mutating the ambient PATH.
var lookPath = exec.LookPath

// ErrBwrapMissing is returned by the Linux install preflight when
// bubblewrap is not on PATH. The message includes distro-specific install
// hints so developer friends hitting this during beta install can fix it
// without a round trip to docs.
var ErrBwrapMissing = fmt.Errorf(
	"bwrap not found; install bubblewrap (apt install bubblewrap / dnf install bubblewrap / pacman -S bubblewrap) — see docs/install.md",
)

// Preflight verifies host prerequisites required by Nanite's sandbox.
// On Linux, bubblewrap (`bwrap`) must be on PATH because `AgentExec`
// relies on it for OS-level isolation. On other platforms this is a
// no-op. Does not attempt to install anything automatically.
func Preflight() error {
	if runtime.GOOS == "linux" {
		return checkBwrap()
	}
	return nil
}

// checkBwrap verifies that `bwrap` resolves on PATH. Separated so tests
// can exercise the Linux path regardless of the host OS by stubbing
// lookPath. Returns ErrBwrapMissing when the binary is not found.
func checkBwrap() error {
	if _, err := lookPath("bwrap"); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return ErrBwrapMissing
		}
		return fmt.Errorf("bwrap preflight: %w", err)
	}
	return nil
}
