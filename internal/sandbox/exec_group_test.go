//go:build unix

package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestAgentExec_TimeoutReapsGrandchildren regression-tests audit finding
// 07. Before the fix, exec.CommandContext sent SIGKILL to the direct
// child only; a shell grandchild (the actual work) would orphan to
// launchd/init and keep running. With Setpgid + Cancel sending SIGTERM
// to the negative PID, the whole group dies within WaitDelay.
//
// Skipped on Linux when bwrap is not present because the sandboxed
// shell path is the only meaningful coverage — the denylist blocks
// direct bash invocation, so we use /bin/sh via absolute path which
// the allowlist permits.
func TestAgentExec_TimeoutReapsGrandchildren(t *testing.T) {
	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("bwrap"); err != nil {
			t.Skip("bwrap not installed; namespaces would prevent observing PID externally")
		}
	}

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Resolve the sandbox dir so the pidfile sits inside it — the
	// macOS seatbelt profile denies writes outside the sandbox dir.
	sandboxDir, err := Dir("test-reap")
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	pidFile := filepath.Join(sandboxDir, "grandchild.pid")

	// Shell script that forks a long sleep in the background and writes
	// its pid to disk. The shell itself then sleeps, so AgentExec's
	// timeout path will kill it.
	//
	// Using absolute paths for sleep / sh / printf since the sandboxed
	// PATH is restricted to /usr/bin, /bin, /usr/local/bin.
	script := `
		/bin/sleep 60 &
		printf '%s' "$!" > ` + pidFile + `
		/bin/sleep 60
	`

	start := time.Now()
	result, err := AgentExec(AgentExecOpts{
		SessionID: "test-reap",
		Command:   "/bin/sh",
		Args:      []string{"-c", script},
		Timeout:   500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("AgentExec: %v", err)
	}
	elapsed := time.Since(start)
	if !result.TimedOut {
		t.Fatalf("expected timeout, got exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	// AgentExec should return within the timeout + WaitDelay + a small
	// scheduling margin — not hang waiting for the orphan.
	if elapsed > 5*time.Second {
		t.Fatalf("AgentExec returned after %v, want <= 5s (WaitDelay bounded)", elapsed)
	}

	// Read the grandchild PID and poll until it's gone. Without the
	// Setpgid + negative-PID SIGTERM, the grandchild sleep-60 would
	// survive for the full minute.
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		// The background sleep may not have written the pid yet — that's
		// test timing, not a regression. Retry briefly.
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
			raw, err = os.ReadFile(pidFile)
			if err == nil {
				break
			}
		}
		if err != nil {
			t.Fatalf("read pid file %q: %v", pidFile, err)
		}
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("parse pid %q: %v", raw, err)
	}

	deadline := time.Now().Add(5 * time.Second)
	var alive bool
	for time.Now().Before(deadline) {
		// signal 0 is the POSIX "does the process exist" probe.
		err := syscall.Kill(pid, 0)
		if err != nil {
			alive = false
			break
		}
		alive = true
		time.Sleep(50 * time.Millisecond)
	}
	if alive {
		// Best-effort cleanup so the test doesn't leak a sleep(60) per
		// failed run on developer laptops.
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("grandchild pid %d still alive 5s after timeout — Setpgid / group-kill regressed", pid)
	}
}

// TestSetProcessGroupKill_CancelReturnsNil regression for the Copilot
// review on PR #19: cmd.Cancel previously returned os.ErrProcessDone
// (via a kill-error wrap), which can mislead exec.CommandContext into
// treating cancellation as completion and short-circuiting Wait. The
// fix returns nil on a successful group-SIGTERM; only a hard kill
// failure (EPERM, etc.) should surface as a non-nil error.
func TestSetProcessGroupKill_CancelReturnsNil(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sleep", "30")
	setProcessGroupKill(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Ensure the child is reaped regardless of how the test exits.
	defer func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	}()

	if err := cmd.Cancel(); err != nil {
		t.Fatalf("cmd.Cancel returned %v, want nil (returning ErrProcessDone or other errors confuses exec.CommandContext)", err)
	}
}
