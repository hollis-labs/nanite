//go:build linux

package sandbox

import (
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"sync"
)

var bwrapWarnOnce sync.Once

// applyOSSandbox wraps the command with Linux bubblewrap (bwrap) for OS-level isolation.
// The original command becomes an argument to bwrap.
// The returned cleanup function is a no-op since bwrap needs no temp files.
func applyOSSandbox(cmd *exec.Cmd, sandboxDir string, networkAllow []string) (cleanup func(), err error) {
	bwrapPath, lookErr := exec.LookPath("bwrap")
	if lookErr != nil {
		bwrapWarnOnce.Do(func() {
			log.Println("sandbox: bwrap not found — install bubblewrap for OS-level isolation (using Tier 1 only)")
		})
		return func() {}, nil
	}

	absDir, err := filepath.Abs(sandboxDir)
	if err != nil {
		return nil, fmt.Errorf("resolve sandbox dir: %w", err)
	}

	// Build bwrap argument list.
	bwrapArgs := []string{
		"bwrap",
		"--ro-bind", "/", "/", // read-only view of entire filesystem
		"--bind", absDir, absDir, // writable sandbox directory
		"--bind", "/tmp", "/tmp", // writable tmp
		"--dev", "/dev", // minimal /dev
		"--proc", "/proc", // process introspection
	}

	// Network isolation: deny all unless networkAllow is non-empty.
	if len(networkAllow) == 0 {
		bwrapArgs = append(bwrapArgs, "--unshare-net")
	}

	bwrapArgs = append(bwrapArgs, "--die-with-parent")
	bwrapArgs = append(bwrapArgs, "--")

	// Append original command and its arguments.
	origPath := cmd.Path
	origArgs := cmd.Args[1:] // Args[0] is the command name

	bwrapArgs = append(bwrapArgs, origPath)
	bwrapArgs = append(bwrapArgs, origArgs...)

	cmd.Path = bwrapPath
	cmd.Args = bwrapArgs

	return func() {}, nil
}
