package shell

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/hollis-labs/nanite/internal/truncate"
)

// DefaultTimeout is the maximum wall-clock time a shell command may run.
const DefaultTimeout = 30 * time.Second

// ExecResult holds the output and metadata of a shell command execution.
type ExecResult struct {
	Command    string `json:"command"`
	Output     string `json:"output"`
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
	Truncated  bool   `json:"truncated"`
}

// ExecOpts configures a single command execution.
type ExecOpts struct {
	// WorkDir is the working directory. If empty, defaults to $HOME.
	WorkDir string
	// Timeout overrides the default 30s timeout. Zero means use DefaultTimeout.
	Timeout time.Duration
}

// Exec runs a shell command, captures combined stdout+stderr, and truncates
// the output via the truncate pipeline. The command is executed via the user's
// shell (SHELL env, fallback /bin/sh).
func Exec(ctx context.Context, command string, opts ExecOpts) *ExecResult {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	cmd := exec.CommandContext(ctx, shell, "-c", command)

	// Set working directory.
	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	} else {
		home, _ := os.UserHomeDir()
		cmd.Dir = home
	}

	// Capture combined stdout + stderr.
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)

	exitCode := 0
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			// Timeout — use standard exit code 124 regardless of the signal code.
			exitCode = 124
			fmt.Fprintf(&buf, "\n[command timed out after %s]", timeout)
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
			fmt.Fprintf(&buf, "\n[exec error: %s]", err.Error())
		}
	}

	// Truncate output via the standard pipeline.
	raw := buf.String()
	tr := truncate.Output(raw, "shell:"+command)

	return &ExecResult{
		Command:    command,
		Output:     tr.Content,
		ExitCode:   exitCode,
		DurationMs: elapsed.Milliseconds(),
		Truncated:  tr.Truncated,
	}
}
