//go:build !unix

package sandbox

import "os/exec"

// setProcessGroupKill is a no-op on non-unix platforms. The process-group
// escalation path is only meaningful where syscall.Kill with a negative
// PID is supported (Linux, macOS, *BSD). Windows has its own job-object
// model which Nanite's sandbox does not currently target.
func setProcessGroupKill(cmd *exec.Cmd) {}
