//go:build !unix

package subprocess

import "os/exec"

// configureSubprocAttr is a no-op on non-unix builds. The subprocess
// plugin manager does not currently target Windows; the process-group
// teardown in Manager.Stop expects POSIX semantics (negative-PID kill).
func configureSubprocAttr(cmd *exec.Cmd) {}
