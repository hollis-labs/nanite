package shell

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// Exec runs one executable directly (no shell), with input on standard
// input. Env is the whole environment: nothing else is inherited.
type Exec struct {
	// Name is what errors call the run, such as "tool package queue-peek".
	Name    string
	Path    string
	Dir     string
	Env     []string
	Stdin   []byte
	Timeout time.Duration
	// MaxStdout caps what is kept of standard output (default 64 KiB); a
	// result that exceeded it reports StdoutTruncated.
	MaxStdout int
}

// ExecResult is what an Exec run wrote and how it exited.
type ExecResult struct {
	Stdout, Stderr  string
	StdoutTruncated bool
	ExitCode        int
}

// maxStderr bounds the standard error kept from an Exec run.
const maxStderr = 8 << 10

// RunExec runs e and waits for it. It returns ctx's error (or
// context.DeadlineExceeded at the timeout) when the run was cut short.
func RunExec(ctx context.Context, e Exec) (ExecResult, error) {
	limit := e.MaxStdout
	if limit <= 0 {
		limit = maxOutput
	}
	stdout, stderr := &limitedBuffer{limit: limit}, &limitedBuffer{limit: maxStderr}
	process, err := startExec(ctx, e, stdout, stderr)
	if err != nil {
		return ExecResult{}, err
	}
	result, err := process.Wait()
	process.CleanupGroup()
	return ExecResult{Stdout: strings.ToValidUTF8(stdout.data.String(), "�"), Stderr: strings.ToValidUTF8(stderr.data.String(), "�"),
		StdoutTruncated: stdout.truncated, ExitCode: result.ExitCode}, err
}

// StartExecStreaming starts e with its standard output and error both
// written to w, for a background job. The caller must Wait exactly once.
func StartExecStreaming(ctx context.Context, e Exec, w io.Writer) (*Process, error) {
	return startExec(ctx, e, w, w)
}

func startExec(ctx context.Context, e Exec, stdout, stderr io.Writer) (*Process, error) {
	label := e.Name
	if label == "" {
		label = e.Path
	}
	process, err := startProcess(ctx, e.Timeout, e.Dir, e.Env, bytes.NewReader(e.Stdin), stdout, stderr, func(runCtx context.Context) *exec.Cmd {
		return exec.CommandContext(runCtx, e.Path) // #nosec G204 -- an authored tool package's entry, run after policy checks.
	})
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", label, err)
	}
	process.label = label
	return process, nil
}

type limitedBuffer struct {
	data      bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	allowed := max(0, min(len(p), b.limit-b.data.Len()))
	_, _ = b.data.Write(p[:allowed])
	b.truncated = b.truncated || allowed < len(p)
	return len(p), nil
}
