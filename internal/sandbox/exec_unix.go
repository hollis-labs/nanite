//go:build unix

package sandbox

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/hollis-labs/nanite/internal/safego"
)

// execWaitDelay bounds the time exec.Cmd.Wait will block after the
// process's context is cancelled. Go 1.22+ uses this to force-close
// stdio pipes that an orphaned grandchild may still hold, so Wait
// cannot hang forever. Small enough that a timed-out exec surfaces
// promptly; large enough that a well-behaved interpreter can flush
// buffered output.
const execWaitDelay = 2 * time.Second

// execGroupKillGrace is the extra time after WaitDelay before we force
// a group-wide SIGKILL. Go's internal WaitDelay escalation targets the
// direct child PID (not the process group), so grandchildren can survive
// its SIGKILL. We backstop with syscall.Kill(-pid, SIGKILL) after this
// grace window.
const execGroupKillGrace = 250 * time.Millisecond

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
//     pipe cannot pin the call forever. Note: Go's internal WaitDelay
//     escalation sends SIGKILL to the direct child PID, not the process
//     group, so grandchildren can survive it. We run our own group-wide
//     SIGKILL timer (spawned via safego.Go) as a backstop.
func setProcessGroupKill(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true

	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		pid := cmd.Process.Pid
		// Negative PID = whole process group. SIGTERM first to let
		// interpreters clean up.
		if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
			// Real failure (e.g. EPERM). Surface it so the caller sees
			// why cancellation didn't take effect.
			return err
		}

		// Schedule a group-wide SIGKILL backstop. Go's internal
		// WaitDelay escalation only SIGKILLs the direct child, so a
		// grandchild holding inherited fds would otherwise survive.
		safego.Go(context.Background(), "sandbox.exec.group-kill", func() {
			time.Sleep(execWaitDelay + execGroupKillGrace)
			// Probe: signal 0 returns nil if any process in the group
			// still exists. ESRCH means the whole group is gone.
			if err := syscall.Kill(-pid, 0); err != nil {
				return
			}
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		})

		// Return nil so exec.CommandContext does NOT treat the Cancel
		// as a completed wait. Returning os.ErrProcessDone (or any
		// error) here can mislead the runtime into short-circuiting
		// the Wait path; nil lets Go's standard WaitDelay + our
		// group-kill backstop run as intended.
		return nil
	}
	cmd.WaitDelay = execWaitDelay
}
