package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var ErrPythonIsolationUnavailable = errors.New("python execution requires OS network isolation")

// PreparePythonCommand applies the existing OS sandbox with no network allowlist
// and a fresh caller-owned scratch directory. It never accepts degraded execution.
// It grants no tool or actor authority: the host must admit the run separately.
// Filesystem restrictions are those of the platform backend; Darwin permits host
// reads. Unlike AgentExec, this preserves the caller's stdin and tool-channel FDs.
func PreparePythonCommand(cmd *exec.Cmd, scratchDir string) (func(), error) {
	if cmd == nil || cmd.Cancel == nil || len(cmd.Args) == 0 || !filepath.IsAbs(scratchDir) {
		return nil, fmt.Errorf("%w: invalid command or scratch directory", ErrPythonIsolationUnavailable)
	}
	info, err := os.Stat(scratchDir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("%w: scratch directory unavailable", ErrPythonIsolationUnavailable)
	}
	// Check before the general backend's operator opt-in can select degradation.
	switch runtime.GOOS {
	case "linux":
		if _, lookupErr := exec.LookPath("bwrap"); lookupErr != nil {
			return nil, ErrPythonIsolationUnavailable
		}
	case "darwin":
		if _, statErr := os.Stat("/usr/bin/sandbox-exec"); statErr != nil {
			return nil, ErrPythonIsolationUnavailable
		}
	default:
		return nil, ErrPythonIsolationUnavailable
	}
	cmd.Dir = scratchDir
	cmd.Env = []string{
		"PATH=" + strings.Join(essentialBinDirs, string(os.PathListSeparator)),
		"LANG=C.UTF-8", "HOME=" + scratchDir, "TMPDIR=" + scratchDir,
	}
	cleanup, isolated, err := applyOSSandbox(cmd, scratchDir, "", nil, "")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPythonIsolationUnavailable, err)
	}
	if !isolated {
		cleanup()
		return nil, ErrPythonIsolationUnavailable
	}

	setPythonProcessGroupKill(cmd)
	return cleanup, nil
}
