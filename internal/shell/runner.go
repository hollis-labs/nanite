// Package shell executes explicitly approved workspace commands.
package shell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/hollis-labs/nanite/internal/processenv"
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
	process, err := Start(ctx, command)
	if err != nil {
		return Result{}, err
	}
	return process.Wait()
}

// Process is a started shell command. Its caller must Wait exactly once.
type Process struct {
	cmd     *exec.Cmd
	ctx     context.Context
	release context.CancelFunc
	output  *cappedOutput
}

func Start(ctx context.Context, command Command) (*Process, error) {
	runCtx, cancel := context.WithTimeout(ctx, command.Timeout)
	cmd := exec.CommandContext(runCtx, "bash", "--noprofile", "--norc", "-c", command.Script)
	cmd.Dir = command.Dir
	cmd.Env = processenv.List(processenv.Build(command.Env))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return terminateGroup(cmd.Process.Pid) }
	cmd.WaitDelay = 2 * time.Second
	output := &cappedOutput{}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start bash: %w", err)
	}
	return &Process{cmd: cmd, ctx: runCtx, release: cancel, output: output}, nil
}

func (p *Process) PID() int { return p.cmd.Process.Pid }

func (p *Process) Wait() (Result, error) {
	defer p.release()
	err := p.cmd.Wait()
	result := Result{Output: p.output.String()}
	if p.ctx.Err() != nil {
		return result, p.ctx.Err()
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

// CleanupGroup terminates descendants still present after background job exit.
func (p *Process) CleanupGroup() { _ = terminateGroup(p.PID()) }

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
