//go:build !unix

package sandbox

import "os/exec"

// PreparePythonCommand refuses unsupported platforms before this is reached.
func setPythonProcessGroupKill(*exec.Cmd) {}
