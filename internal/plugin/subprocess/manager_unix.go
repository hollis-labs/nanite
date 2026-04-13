//go:build unix

package subprocess

import (
	"os/exec"
	"syscall"
)

// configureSubprocAttr sets Setpgid so the subprocess plugin and any
// helpers it forks are placed in a fresh process group. Manager.Stop
// uses the negative-PID form of syscall.Kill to signal the whole group,
// which is the only way to reap grandchildren that the plugin's runtime
// (node, python, etc.) may spawn. See audit finding 07.
func configureSubprocAttr(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}
