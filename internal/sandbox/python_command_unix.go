//go:build unix

package sandbox

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Python cancellation has no graceful background cleanup phase. Kill the group
// immediately, then bound inherited stdio drain without scheduling a delayed
// signal against a process group that could have already exited or been reused.
func setPythonProcessGroupKill(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			if err == syscall.ESRCH {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
	cmd.WaitDelay = 500 * time.Millisecond
}
