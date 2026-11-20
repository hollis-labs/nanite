// Package shell executes explicitly approved workspace commands.
package shell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
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
	// label names what runs in errors ("bash", or a tool package).
	label   string
	cmd     *exec.Cmd
	ctx     context.Context
	release func()
	output  *cappedOutput
}

func Start(ctx context.Context, command Command) (*Process, error) {
	return start(ctx, command, nil)
}

// StartStreaming sends combined output to a single writer as it is produced.
func StartStreaming(ctx context.Context, command Command, writer io.Writer) (*Process, error) {
	return start(ctx, command, writer)
}

func start(ctx context.Context, command Command, writer io.Writer) (*Process, error) {
	output := &cappedOutput{}
	if writer == nil {
		writer = output
	}
	process, err := startProcess(ctx, command.Timeout, command.Dir, processenv.List(processenv.Build(command.Env)), nil, writer, writer,
		func(runCtx context.Context) *exec.Cmd {
			return exec.CommandContext(runCtx, "bash", "--noprofile", "--norc", "-c", command.Script) // #nosec G204 -- Bash executes an approved command after policy checks.
		})
	if err != nil {
		return nil, fmt.Errorf("start bash: %w", err)
	}
	process.output, process.label = output, "bash"
	return process, nil
}

// startProcess starts a command in its own process group, tied to this
// process by a lifeline and ended (group and all) when ctx is done or
// timeout passes.
func startProcess(ctx context.Context, timeout time.Duration, dir string, env []string, stdin io.Reader, stdout, stderr io.Writer, build func(context.Context) *exec.Cmd) (*Process, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	cmd := build(runCtx)
	cmd.Dir = dir
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return terminateGroup(cmd.Process.Pid) }
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	stopLifeline := startLifeline(cmd.Process.Pid)
	return &Process{cmd: cmd, ctx: runCtx, release: func() { stopLifeline(); cancel() }, output: &cappedOutput{}}, nil
}

// lifelineScript terminates a process group once its standard input closes.
// It avoids `kill --`, which dash rejects.
const lifelineScript = `read -r _; kill -TERM "-$1" 2>/dev/null; sleep 1; kill -KILL "-$1" 2>/dev/null`

// startLifeline ties the command's process group to this process. A watcher
// holds the read end of a pipe whose only write end lives here, so the group
// is terminated even when this process is killed and can run no cleanup. The
// returned function retires the watcher after normal completion. A watcher
// that cannot start leaves the command running as before.
func startLifeline(pgid int) (stop func()) {
	read, write, err := os.Pipe()
	if err != nil {
		return func() {}
	}
	watcher := exec.Command("/bin/sh", "-c", lifelineScript, "nanite-lifeline", strconv.Itoa(pgid)) // #nosec G204 -- Fixed script with a numeric process group.
	watcher.Stdin = read
	watcher.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err = watcher.Start()
	_ = read.Close() // The watcher holds its own copy.
	if err != nil {
		_ = write.Close()
		return func() {}
	}
	return func() {
		// Kill before closing so a finished command's reused group ID is never signaled.
		_ = watcher.Process.Kill()
		_ = write.Close()
		_ = watcher.Wait()
	}
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
		return result, fmt.Errorf("run %s: %w", p.label, err)
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
