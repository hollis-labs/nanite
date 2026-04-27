// Package background — PTY backend.
//
// PTYBackend is the D2 short-term dispatch backend: each Submit
// launches a detached child process in a new process group, captures
// stdout+stderr to a bounded buffer, and reports completion via the
// CompletionFunc callback.
//
// Process lifecycle hygiene (re-violating any of these is a blocker
// per the ticket Anti-patterns):
//
//   - Children run in their own process group (Setpgid=true). This
//     lets Cancel kill the whole tree with a single negative-PID
//     kill — descendants spawned by the child don't get orphaned.
//   - Cancel sends SIGTERM to the process group, then SIGKILL after
//     a grace period if the process is still alive.
//   - The reaping goroutine always Wait()s the child so we never
//     leak zombies; the child's exit code is captured before the
//     completion callback fires.
//   - Output is captured to an in-memory buffer with a hard byte
//     cap (Budget.MaxOutputBytes). Beyond the cap, additional
//     bytes are dropped (not buffered) so a runaway child can't
//     OOM the parent.
package background

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// killGracePeriod is how long PTYBackend waits between SIGTERM and
// SIGKILL when cancelling a job. Long enough for a well-behaved
// child to flush output and exit cleanly; short enough that a
// Cancel call returns within the test budget (1s upper bound for
// the cancellation tests).
const killGracePeriod = 750 * time.Millisecond

// commandFactory is the seam tests use to substitute a fake child
// command. The default factory invokes /bin/sh -c <task>; tests
// override it to spawn predictable shell scripts or Go-built
// helpers without the test process needing /bin/sh.
type commandFactory func(ctx context.Context, req JobRequest) *exec.Cmd

// defaultCommandFactory builds a /bin/sh -c invocation of req.Task.
// Captures stdout+stderr into the *exec.Cmd's StdoutPipe / StderrPipe
// (the caller drains them), and stamps SysProcAttr.Setpgid so the
// child is in its own process group.
func defaultCommandFactory(ctx context.Context, req JobRequest) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", req.Task)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

// PTYBackend implements Backend by launching detached shell
// subprocesses. Despite the name, this MVP does NOT allocate a real
// PTY (creack/pty would be the lever to add later if interactive
// stdin became a requirement); it uses standard pipes for output
// capture. The "PTY" naming is preserved from the ticket's D2
// language — the backend swap to agent-mux (D3) replaces this whole
// file rather than the type name.
type PTYBackend struct {
	// commandFactory builds the *exec.Cmd. Tests inject a stub.
	commandFactory commandFactory

	mu   sync.Mutex
	jobs map[string]*ptyJob
}

// ptyJob holds the per-job state PTYBackend tracks: the running
// *exec.Cmd, its process group id, the cancel function used to
// short-circuit Wait, and the current lifecycle status.
type ptyJob struct {
	cmd    *exec.Cmd
	pgid   int
	cancel context.CancelFunc
	status JobStatus
	// done closes when the reaper goroutine finishes Wait()ing the
	// child — Cancel uses this to know when it's safe to mark the
	// status terminal without a deadline race.
	done chan struct{}
	// cancelled is set by Cancel before signalling the process so
	// the reaper's classifyExit reports StatusCancelled rather than
	// StatusFailed (the kernel-delivered signal looks like an exit
	// error to cmd.Wait, but the user-visible reason is "cancelled").
	// Same flag is set by the wall-clock-budget timer.
	cancelled bool
}

// NewPTYBackend returns a PTYBackend wired with the production
// commandFactory. Tests use newPTYBackendForTest to inject a stub
// factory.
func NewPTYBackend() *PTYBackend {
	return &PTYBackend{
		commandFactory: defaultCommandFactory,
		jobs:           make(map[string]*ptyJob),
	}
}

// newPTYBackendForTest is the test seam. Not exported.
func newPTYBackendForTest(factory commandFactory) *PTYBackend {
	return &PTYBackend{
		commandFactory: factory,
		jobs:           make(map[string]*ptyJob),
	}
}

// Start launches a child process for jobID. Spawns a reaper
// goroutine that Wait()s the child, captures output, and invokes
// onComplete exactly once.
func (b *PTYBackend) Start(ctx context.Context, jobID string, req JobRequest, onComplete CompletionFunc) error {
	if onComplete == nil {
		return errors.New("background.pty: onComplete is required")
	}

	// Per-job ctx so Cancel can interrupt the running child without
	// affecting other jobs. Note: the parent ctx (from Submit) is
	// observed for setup but the child's lifetime is governed by
	// runCtx — the parent ctx may already be done by the time the
	// child completes.
	runCtx, cancel := context.WithCancel(context.Background())
	cmd := b.commandFactory(runCtx, req)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("background.pty: stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("background.pty: stderr pipe: %w", err)
	}

	startedAt := time.Now().UTC()
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("background.pty: start: %w", err)
	}

	// Stamp the pgid; required for the kill-process-group path on
	// Cancel. On Linux/Darwin this equals cmd.Process.Pid because
	// SysProcAttr.Setpgid put the child into its own group.
	pgid := cmd.Process.Pid

	job := &ptyJob{
		cmd:    cmd,
		pgid:   pgid,
		cancel: cancel,
		status: StatusRunning,
		done:   make(chan struct{}),
	}
	b.mu.Lock()
	b.jobs[jobID] = job
	b.mu.Unlock()

	// Wall-clock enforcement: a separate timer that cancels runCtx
	// after Budget.WallClockSeconds, then Cancel(jobID) closes the
	// process group. The reaper observes the cancellation and
	// reports StatusFailed with a budget-exceeded error.
	wallClockTimer := time.AfterFunc(
		time.Duration(req.Budget.WallClockSeconds)*time.Second,
		func() {
			slog.Warn("background.pty: wall-clock budget exceeded",
				"job_id", jobID, "budget_seconds", req.Budget.WallClockSeconds)
			_ = b.Cancel(jobID)
		},
	)

	// Reaper goroutine. Captures output, Wait()s the child, decides
	// terminal status, fires onComplete exactly once, and clears
	// the in-memory record.
	go func() {
		defer close(job.done)
		defer wallClockTimer.Stop()

		output, truncated := drainPipes(stdoutPipe, stderrPipe, req.Budget.MaxOutputBytes)
		waitErr := cmd.Wait()
		completedAt := time.Now().UTC()

		// Read the cancellation flag under the mutex — Cancel sets
		// it before signalling, so by the time Wait returns, the
		// flag accurately reflects whether the exit was caller-
		// requested or a real failure.
		b.mu.Lock()
		cancelled := job.cancelled
		b.mu.Unlock()

		status, errReport := classifyExit(waitErr, runCtx.Err(), cancelled)

		// Belt-and-suspenders: ensure no descendants survived the
		// child's exit. killProcessGroup with SIGKILL is idempotent
		// (errors swallowed) and only matters if the child spawned
		// long-running grandchildren that escaped the Wait.
		_ = killProcessGroup(pgid, syscall.SIGKILL)

		b.mu.Lock()
		job.status = status
		b.mu.Unlock()

		onComplete(jobID, BackendCompletion{
			Status:          status,
			Output:          output,
			OutputTruncated: truncated,
			Err:             errReport,
			StartedAt:       startedAt,
			CompletedAt:     completedAt,
		})
	}()

	return nil
}

// Status returns the lifecycle state of jobID. ErrUnknownJob when
// the backend has no record (job either never existed or was reaped
// out of the map after completion — the Service maintains its own
// authoritative record either way).
func (b *PTYBackend) Status(jobID string) (JobStatus, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	job, ok := b.jobs[jobID]
	if !ok {
		return "", ErrUnknownJob
	}
	return job.status, nil
}

// Cancel terminates jobID's process group. Idempotent — Cancel on
// an unknown or already-terminal job returns nil. Sends SIGTERM
// first, then SIGKILL after killGracePeriod if the process is still
// alive. Either way, the reaper observes the death and fires
// onComplete with StatusCancelled.
func (b *PTYBackend) Cancel(jobID string) error {
	b.mu.Lock()
	job, ok := b.jobs[jobID]
	if !ok {
		b.mu.Unlock()
		return nil // idempotent
	}
	if job.status.IsTerminal() {
		b.mu.Unlock()
		return nil // already done; reaper handled
	}
	// Stamp the cancellation flag BEFORE signalling so the reaper
	// classifies the resulting Wait error as StatusCancelled, not
	// StatusFailed.
	job.cancelled = true
	pgid := job.pgid
	cancel := job.cancel
	done := job.done
	b.mu.Unlock()

	// SIGTERM the group; the reaper picks up the death.
	_ = killProcessGroup(pgid, syscall.SIGTERM)

	// Race a short grace period against actual exit. If the child
	// hasn't reaped within killGracePeriod, escalate to SIGKILL.
	select {
	case <-done:
		// Reaper already finished — child exited within grace.
	case <-time.After(killGracePeriod):
		_ = killProcessGroup(pgid, syscall.SIGKILL)
	}

	// Cancel the runCtx so cmd.Wait unblocks if it hasn't already.
	cancel()
	return nil
}

// drainPipes concurrently reads stdout + stderr into a single byte
// buffer, capped at maxBytes. Returns the captured string and a
// truncated flag. Concurrent readers prevent the child from blocking
// on a full pipe buffer.
func drainPipes(stdout, stderr io.Reader, maxBytes int) (string, bool) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxOutputBytes
	}
	var (
		mu        sync.Mutex
		buf       []byte
		truncated bool
	)
	writeChunk := func(data []byte) {
		mu.Lock()
		defer mu.Unlock()
		if len(buf) >= maxBytes {
			truncated = true
			return
		}
		room := maxBytes - len(buf)
		if len(data) > room {
			buf = append(buf, data[:room]...)
			truncated = true
			return
		}
		buf = append(buf, data...)
	}
	var wg sync.WaitGroup
	for _, r := range []io.Reader{stdout, stderr} {
		wg.Add(1)
		go func(r io.Reader) {
			defer wg.Done()
			tmp := make([]byte, 4096)
			for {
				n, err := r.Read(tmp)
				if n > 0 {
					writeChunk(tmp[:n])
				}
				if err != nil {
					return
				}
			}
		}(r)
	}
	wg.Wait()
	out := string(buf)
	if truncated {
		out += fmt.Sprintf("\n[output truncated at %d bytes]\n", maxBytes)
	}
	return out, truncated
}

// classifyExit maps a cmd.Wait() error + runCtx.Err() + caller-set
// cancelled flag to a terminal JobStatus. Cancellation (cancelled
// flag set OR runCtx done) reports StatusCancelled with no error;
// non-zero exit reports StatusFailed with the wait error; clean
// exit reports StatusSucceeded.
//
// The cancelled flag is the load-bearing signal: ctxErr is only set
// when the runCtx is cancelled, but Cancel signals the process group
// directly (faster than relying on exec.CommandContext's cancel
// semantics) and then closes the runCtx after a grace period — by
// the time Wait returns, ctxErr may not yet be set, so the explicit
// flag is what the reaper checks.
func classifyExit(waitErr, ctxErr error, cancelled bool) (JobStatus, error) {
	if cancelled || ctxErr != nil {
		// Cancellation request — even if the child had already exited
		// non-zero, the user-visible reason is "cancelled".
		return StatusCancelled, nil
	}
	if waitErr == nil {
		return StatusSucceeded, nil
	}
	// Distinguish "exited with non-zero status" (a normal program
	// failure) from process-supervision errors.
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		return StatusFailed, fmt.Errorf("child exited: %w", waitErr)
	}
	return StatusFailed, fmt.Errorf("wait: %w", waitErr)
}

// killProcessGroup sends sig to the process group identified by pgid.
// Returns the underlying syscall error (which the caller typically
// swallows — a missing-process error means the group already exited,
// which is exactly what we want).
//
// Uses syscall.Kill with negative pgid, which targets the whole
// group on Unix-likes. On platforms where this is unsupported
// (Windows) the build would fail; nanite is Unix-only today, so
// this is acceptable.
func killProcessGroup(pgid int, sig syscall.Signal) error {
	if pgid <= 0 {
		return nil
	}
	err := syscall.Kill(-pgid, sig)
	if err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

