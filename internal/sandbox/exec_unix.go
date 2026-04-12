//go:build unix

package sandbox

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// execWaitDelay bounds the time exec.Cmd.Wait will block after the
// process's context is cancelled. Go 1.22+ uses this to force-close
// stdio pipes that an orphaned grandchild may still hold, so Wait
// cannot hang forever. Small enough that a timed-out exec surfaces
// promptly; large enough that a well-behaved interpreter can flush
// buffered output.
const execWaitDelay = 2 * time.Second

// setProcessGroupKill configures cmd so the child starts in a fresh
// process group and the whole group is signalled on context cancel.
//
// Rationale (2026-04-10 sandbox-hardening audit, finding 07):
//
//   - macOS: sandbox-exec is the direct child; the interpreter (sh, python,
//     node) is its child; user scripts may fork further. Go's default
//     exec.CommandContext sends SIGKILL only to the direct child, so
//     interpreter descendants orphan to launchd. Setpgid + negative-PID
//     signalling gets the whole tree.
//   - Linux: bwrap's --die-with-parent already handles this at the namespace
//     level, but the process-group approach is defensive if bwrap is
//     skipped (install missing) or someone calls UserExec without a
//     namespace.
//   - WaitDelay bounds Wait so a grandchild holding an inherited stdout
//     pipe cannot pin the call forever.
func setProcessGroupKill(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true

	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		// Negative PID = whole process group. SIGTERM first to let
		// interpreters clean up; Go's internal WaitDelay escalates to
		// SIGKILL on the whole group via the same kill() path below
		// because the process group id equals the leader's PID.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		return os.ErrProcessDone
	}
	cmd.WaitDelay = execWaitDelay
}
