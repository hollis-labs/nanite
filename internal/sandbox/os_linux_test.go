//go:build linux

package sandbox

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestBwrapArgs_NarrowedMounts verifies the command wrapping puts the
// expected bwrap flags in place: narrowed read-only mounts (no blanket
// `--ro-bind / /`), per-invocation tmpfs for /tmp, and the full set of
// namespace unshare flags. Regression test for audit finding 06.
func TestBwrapArgs_NarrowedMounts(t *testing.T) {
	// Skip if bwrap isn't on this builder — we only assert shape when
	// the wrapper would actually take effect.
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed")
	}

	cmd := exec.Command("/bin/true")
	cleanup, err := applyOSSandbox(cmd, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("applyOSSandbox: %v", err)
	}
	defer cleanup()

	joined := strings.Join(cmd.Args, " ")

	// Must NOT contain the old blanket-mount flag.
	if strings.Contains(joined, "--ro-bind / /") {
		t.Errorf("bwrap still uses blanket `--ro-bind / /`; finding 06 gap #1 regressed\nargs: %s", joined)
	}
	// Must use a per-invocation tmpfs for /tmp, not a host bind.
	if !strings.Contains(joined, "--tmpfs /tmp") {
		t.Errorf("bwrap missing `--tmpfs /tmp`; finding 06 gap #4 regressed\nargs: %s", joined)
	}
	if strings.Contains(joined, "--bind /tmp /tmp") {
		t.Errorf("bwrap still binds host /tmp; finding 06 gap #4 regressed\nargs: %s", joined)
	}
	// Namespace flags required by the audit.
	for _, want := range []string{
		"--unshare-pid",
		"--unshare-ipc",
		"--unshare-uts",
		"--unshare-net",
		"--unshare-user-try",
		"--die-with-parent",
		"--new-session",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("bwrap args missing %q\nargs: %s", want, joined)
		}
	}
	// At least one narrowed read-only bind must be present (usually /usr).
	if !strings.Contains(joined, "--ro-bind /usr /usr") {
		t.Errorf("bwrap args missing narrowed `--ro-bind /usr /usr`\nargs: %s", joined)
	}
}

// TestBwrapIsolation_ProcCannotSeeHostPID1 is a live-execution test:
// spawn a sandboxed command and verify it cannot read the host's init
// comm string via /proc/1/comm. With --unshare-pid, /proc/1 inside
// the sandbox is the sandboxed process itself, not the host's init.
func TestBwrapIsolation_ProcCannotSeeHostPID1(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed")
	}

	t.Setenv("HOME", t.TempDir())
	// cat /proc/1/comm — in the host, this is typically "systemd",
	// "init", or "launchd". Under --unshare-pid the sandboxed process
	// sees itself as pid 1 so /proc/1/comm is "bwrap" or the wrapped
	// binary name, never "systemd".
	res, err := AgentExec(AgentExecOpts{
		SessionID: "test-pidns",
		Command:   "/bin/cat",
		Args:      []string{"/proc/1/comm"},
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("AgentExec: %v", err)
	}
	got := strings.TrimSpace(res.Stdout)
	if got == "systemd" || got == "init" {
		t.Errorf("/proc/1/comm = %q — host PID namespace leaked into sandbox (finding 06 gap #2)", got)
	}
}
