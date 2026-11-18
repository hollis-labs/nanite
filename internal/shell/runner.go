// Package shell executes explicitly approved workspace commands.
package shell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"time"
)

const maxOutput = 64 << 10

type Command struct {
	Script  string
	Dir     string
	Env     map[string]string
	Timeout time.Duration
}

type Result struct {
	Output   string
	ExitCode int
}

type Runner interface {
	Run(context.Context, Command) (Result, error)
}

type Plain struct{}

func (Plain) Run(ctx context.Context, command Command) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, command.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "--noprofile", "--norc", "-c", command.Script)
	cmd.Dir = command.Dir
	cmd.Env = environment(command.Env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return terminateGroup(cmd.Process.Pid) }
	cmd.WaitDelay = 2 * time.Second
	var output cappedOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	result := Result{Output: output.String()}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("run bash: %w", err)
	}
	return result, nil
}

// terminateGroup gives the shell and its children time to handle SIGTERM.
func terminateGroup(pid int) error {
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	deadline := time.NewTimer(750 * time.Millisecond)
	defer deadline.Stop()
	poll := time.NewTicker(25 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case <-poll.C:
			err := syscall.Kill(-pid, 0)
			if errors.Is(err, syscall.ESRCH) {
				return nil
			}
			if err != nil {
				return err
			}
		case <-deadline.C:
			err := syscall.Kill(-pid, syscall.SIGKILL)
			if errors.Is(err, syscall.ESRCH) {
				return nil
			}
			return err
		}
	}
}

func environment(declared map[string]string) []string {
	env := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		switch key {
		case "PATH", "HOME", "USER", "LANG", "TERM", "TMPDIR", "SHELL":
			env[key] = value
		default:
			if strings.HasPrefix(key, "LC_") {
				env[key] = value
			}
		}
	}
	for key, value := range declared {
		env[key] = value
	}
	out := make([]string, 0, len(env))
	for key, value := range env {
		out = append(out, key+"="+value)
	}
	slices.Sort(out)
	return out
}

type cappedOutput struct {
	data      bytes.Buffer
	truncated bool
}

func (b *cappedOutput) Write(p []byte) (int, error) {
	allowed := min(len(p), maxOutput-b.data.Len())
	_, _ = b.data.Write(p[:allowed])
	b.truncated = b.truncated || allowed < len(p)
	return len(p), nil
}

func (b *cappedOutput) String() string {
	text := b.data.String()
	if b.truncated {
		text += "\n[output truncated]"
	}
	return strings.ToValidUTF8(text, "�")
}
