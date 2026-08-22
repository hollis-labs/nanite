package background

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// PTY tests rely on /bin/sh being available. nanite is Unix-only;
// skip on Windows defensively in case someone tries to run them via
// cross-build CI.
func skipIfWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("PTYBackend requires Unix process-group semantics")
	}
}

// captureCompletion is the test-side onComplete: stores the final
// BackendCompletion and signals via a channel when received.
type captureCompletion struct {
	mu   sync.Mutex
	got  BackendCompletion
	ch   chan struct{}
	once sync.Once
}

func newCapture() *captureCompletion {
	return &captureCompletion{ch: make(chan struct{})}
}

func (c *captureCompletion) callback(_ string, completion BackendCompletion) {
	c.mu.Lock()
	c.got = completion
	c.mu.Unlock()
	c.once.Do(func() { close(c.ch) })
}

func (c *captureCompletion) wait(t *testing.T, timeout time.Duration) BackendCompletion {
	t.Helper()
	select {
	case <-c.ch:
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.got
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for completion after %s", timeout)
		return BackendCompletion{}
	}
}

// reapAll cancels every job tracked by b. Used as a t.Cleanup so a
// failing test doesn't leak processes.
func reapAll(b *PTYBackend) {
	b.mu.Lock()
	ids := make([]string, 0, len(b.jobs))
	for id := range b.jobs {
		ids = append(ids, id)
	}
	b.mu.Unlock()
	for _, id := range ids {
		_ = b.Cancel(id)
	}
}

func TestPTYBackend_SuccessCapturesOutput(t *testing.T) {
	skipIfWindows(t)
	t.Parallel()

	be := NewPTYBackend()
	t.Cleanup(func() { reapAll(be) })

	cap := newCapture()
	req := JobRequest{
		Task:                 "echo hello-pty",
		Budget:               JobBudget{WallClockSeconds: 5, MaxOutputBytes: DefaultMaxOutputBytes},
		OriginatingSessionID: "s",
		OriginatingAgentID:   "a",
	}
	if err := be.Start(context.Background(), "job-1", req, cap.callback); err != nil {
		t.Fatalf("Start: %v", err)
	}

	got := cap.wait(t, 5*time.Second)
	if got.Status != StatusSucceeded {
		t.Fatalf("status = %s; want succeeded", got.Status)
	}
	if !strings.Contains(got.Output, "hello-pty") {
		t.Fatalf("output = %q; want contains hello-pty", got.Output)
	}
	if got.OutputTruncated {
		t.Fatalf("output unexpectedly truncated")
	}
	if got.StartedAt.IsZero() || got.CompletedAt.IsZero() {
		t.Fatalf("missing timestamps: started=%v completed=%v", got.StartedAt, got.CompletedAt)
	}
}

func TestPTYBackend_NonZeroExitReportsFailed(t *testing.T) {
	skipIfWindows(t)
	t.Parallel()

	be := NewPTYBackend()
	t.Cleanup(func() { reapAll(be) })

	cap := newCapture()
	req := JobRequest{
		Task:                 "exit 7",
		Budget:               JobBudget{WallClockSeconds: 5, MaxOutputBytes: DefaultMaxOutputBytes},
		OriginatingSessionID: "s",
		OriginatingAgentID:   "a",
	}
	if err := be.Start(context.Background(), "job-x", req, cap.callback); err != nil {
		t.Fatalf("Start: %v", err)
	}
	got := cap.wait(t, 5*time.Second)
	if got.Status != StatusFailed {
		t.Fatalf("status = %s; want failed", got.Status)
	}
	if got.Err == nil {
		t.Fatalf("expected Err set on failure")
	}
}

func TestPTYBackend_CancelKillsProcessGroup(t *testing.T) {
	skipIfWindows(t)
	t.Parallel()

	be := NewPTYBackend()
	t.Cleanup(func() { reapAll(be) })

	cap := newCapture()
	// Start a long-running shell; Cancel should kill it within ~1s.
	req := JobRequest{
		Task:                 "sleep 30",
		Budget:               JobBudget{WallClockSeconds: 60, MaxOutputBytes: DefaultMaxOutputBytes},
		OriginatingSessionID: "s",
		OriginatingAgentID:   "a",
	}
	if err := be.Start(context.Background(), "job-c", req, cap.callback); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Capture pgid before cancel — we want to verify the group is gone after.
	be.mu.Lock()
	pgid := be.jobs["job-c"].pgid
	be.mu.Unlock()
	if pgid <= 0 {
		t.Fatalf("expected pgid > 0; got %d", pgid)
	}

	cancelStart := time.Now()
	if err := be.Cancel("job-c"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	got := cap.wait(t, 2*time.Second)
	if got.Status != StatusCancelled {
		t.Fatalf("status = %s; want cancelled", got.Status)
	}
	if elapsed := time.Since(cancelStart); elapsed > 2*time.Second {
		t.Fatalf("Cancel took %s; expected <2s", elapsed)
	}

	// Process group should be gone — Kill(-pgid, 0) returns ESRCH
	// when the group has no members.
	if err := syscall.Kill(-pgid, 0); err != syscall.ESRCH {
		t.Fatalf("kill -%d, 0 = %v; want ESRCH (group already reaped)", pgid, err)
	}
}

func TestPTYBackend_CancelUnknownIsIdempotent(t *testing.T) {
	skipIfWindows(t)
	t.Parallel()
	be := NewPTYBackend()
	if err := be.Cancel("nope"); err != nil {
		t.Fatalf("Cancel(unknown) = %v; want nil", err)
	}
}

func TestPTYBackend_StatusUnknown(t *testing.T) {
	skipIfWindows(t)
	t.Parallel()
	be := NewPTYBackend()
	_, err := be.Status("nope")
	if err == nil {
		t.Fatal("expected ErrUnknownJob")
	}
}

func TestPTYBackend_OutputTruncatedAtMaxBytes(t *testing.T) {
	skipIfWindows(t)
	t.Parallel()

	be := NewPTYBackend()
	t.Cleanup(func() { reapAll(be) })

	cap := newCapture()
	// yes outputs "y\ny\n" forever — bounded by output budget.
	req := JobRequest{
		Task:                 "yes y | head -c 4096",
		Budget:               JobBudget{WallClockSeconds: 5, MaxOutputBytes: 256},
		OriginatingSessionID: "s",
		OriginatingAgentID:   "a",
	}
	if err := be.Start(context.Background(), "job-trunc", req, cap.callback); err != nil {
		t.Fatalf("Start: %v", err)
	}
	got := cap.wait(t, 5*time.Second)
	if got.Status != StatusSucceeded {
		t.Fatalf("status = %s; want succeeded", got.Status)
	}
	if !got.OutputTruncated {
		t.Fatalf("expected OutputTruncated=true")
	}
}

// TestPTYBackend_LongRunningJobLifecycle is the acceptance scenario
// from the ticket: run a long-running job and verify the result
// envelope arrives with the captured output. The ticket nominally
// wants 2 minutes; we use 5s here so race-CI stays fast — same
// lifecycle, smaller wall-clock budget. Run with -short to skip.
func TestPTYBackend_LongRunningJobLifecycle(t *testing.T) {
	skipIfWindows(t)
	if testing.Short() {
		t.Skip("skipping long-running job test in -short mode")
	}
	t.Parallel()

	be := NewPTYBackend()
	t.Cleanup(func() { reapAll(be) })

	cap := newCapture()
	// Loop that prints 5 timestamps over ~5s — exercises the
	// streaming-capture path while staying CI-friendly.
	req := JobRequest{
		Task:                 `for i in 1 2 3 4 5; do echo "tick $i"; sleep 1; done`,
		Budget:               JobBudget{WallClockSeconds: 30, MaxOutputBytes: DefaultMaxOutputBytes},
		OriginatingSessionID: "s",
		OriginatingAgentID:   "a",
	}
	start := time.Now()
	if err := be.Start(context.Background(), "job-long", req, cap.callback); err != nil {
		t.Fatalf("Start: %v", err)
	}
	got := cap.wait(t, 15*time.Second)
	if got.Status != StatusSucceeded {
		t.Fatalf("status = %s err=%v output=%q", got.Status, got.Err, got.Output)
	}
	if !strings.Contains(got.Output, "tick 1") || !strings.Contains(got.Output, "tick 5") {
		t.Fatalf("output missing ticks: %q", got.Output)
	}
	if elapsed := time.Since(start); elapsed < 4*time.Second {
		t.Fatalf("job completed in %s; expected ≥4s of work", elapsed)
	}
}

// TestPTYBackend_WallClockBudgetCancels verifies the wall-clock
// guard fires when the job exceeds Budget.WallClockSeconds, and the
// completion envelope reports cancelled.
func TestPTYBackend_WallClockBudgetCancels(t *testing.T) {
	skipIfWindows(t)
	t.Parallel()

	be := NewPTYBackend()
	t.Cleanup(func() { reapAll(be) })

	cap := newCapture()
	req := JobRequest{
		Task: "sleep 10",
		// 1 second budget — wall-clock kicks in and cancels.
		Budget:               JobBudget{WallClockSeconds: 1, MaxOutputBytes: DefaultMaxOutputBytes},
		OriginatingSessionID: "s",
		OriginatingAgentID:   "a",
	}
	if err := be.Start(context.Background(), "job-wc", req, cap.callback); err != nil {
		t.Fatalf("Start: %v", err)
	}
	got := cap.wait(t, 5*time.Second)
	if got.Status != StatusCancelled {
		t.Fatalf("status = %s; want cancelled (wall-clock budget)", got.Status)
	}
}

// TestPTYBackend_NoOrphanZombies verifies that after job completion
// the child PID is fully reaped — Wait was called, no <defunct>
// state remains. We probe by sending signal 0 to the PID; ESRCH
// means the kernel has cleaned up.
func TestPTYBackend_NoOrphanZombies(t *testing.T) {
	skipIfWindows(t)
	t.Parallel()

	be := NewPTYBackend()
	t.Cleanup(func() { reapAll(be) })

	cap := newCapture()
	req := JobRequest{
		Task:                 "true",
		Budget:               JobBudget{WallClockSeconds: 5, MaxOutputBytes: DefaultMaxOutputBytes},
		OriginatingSessionID: "s",
		OriginatingAgentID:   "a",
	}
	if err := be.Start(context.Background(), "job-z", req, cap.callback); err != nil {
		t.Fatalf("Start: %v", err)
	}
	be.mu.Lock()
	pid := be.jobs["job-z"].cmd.Process.Pid
	be.mu.Unlock()
	cap.wait(t, 5*time.Second)

	// The process should be reaped even though the backend's process
	// record has already been released after completion. Kill(pid, 0)
	// returns ESRCH once the kernel has cleaned it up.
	err := syscall.Kill(pid, 0)
	if err != syscall.ESRCH {
		t.Fatalf("kill(%d, 0) = %v; want ESRCH (process should be reaped)", pid, err)
	}
}

func TestPTYBackend_RemovesCompletedJobRecord(t *testing.T) {
	skipIfWindows(t)
	t.Parallel()

	be := NewPTYBackend()
	cap := newCapture()
	req := JobRequest{
		Task:                 "true",
		Budget:               JobBudget{WallClockSeconds: 5, MaxOutputBytes: DefaultMaxOutputBytes},
		OriginatingSessionID: "s",
		OriginatingAgentID:   "a",
	}
	if err := be.Start(context.Background(), "job-retention", req, cap.callback); err != nil {
		t.Fatalf("Start: %v", err)
	}
	cap.wait(t, 5*time.Second)

	be.mu.Lock()
	defer be.mu.Unlock()
	if _, ok := be.jobs["job-retention"]; ok {
		t.Fatal("completed backend job remains retained")
	}
}

// TestDefaultCommandFactory_BuildsShInvocation pins the production
// factory's shape so a refactor doesn't silently change the shell
// invocation contract.
func TestDefaultCommandFactory_BuildsShInvocation(t *testing.T) {
	skipIfWindows(t)
	t.Parallel()
	cmd := defaultCommandFactory(context.Background(), JobRequest{Task: "echo x"})
	if cmd.Path == "" {
		t.Fatal("expected cmd.Path set")
	}
	wantSuffix := "/sh"
	if !strings.HasSuffix(cmd.Path, wantSuffix) {
		t.Fatalf("cmd.Path = %q; want suffix %q", cmd.Path, wantSuffix)
	}
	if got := cmd.SysProcAttr; got == nil || !got.Setpgid {
		t.Fatalf("SysProcAttr.Setpgid = %v; want true", got)
	}
	// args[1] should be -c, args[2] the task.
	if len(cmd.Args) < 3 || cmd.Args[1] != "-c" || cmd.Args[2] != "echo x" {
		t.Fatalf("cmd.Args = %v; want [..., -c, echo x]", cmd.Args)
	}
}

// silence unused; the import is kept for potential future tests.
var _ = exec.ErrNotFound
