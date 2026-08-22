//go:build linux

package sandbox

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	cleanup, isolated, err := applyOSSandbox(cmd, t.TempDir(), "", nil, "")
	if err != nil {
		t.Fatalf("applyOSSandbox: %v", err)
	}
	defer cleanup()
	if !isolated {
		t.Error("applyOSSandbox with bwrap present: isolated = false, want true")
	}

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

// TestBwrapArgs_UnshareNetUnconditional is AD-02's regression test
// (TASKS/audit-remediation/ARCHITECT-DECISIONS.md, decided 2026-08-22),
// replacing the old TestBwrapArgs_UnshareNetConditional this superseded:
// --unshare-net must now be present REGARDLESS of whether networkAllow is
// empty. Before this fix, a non-empty allowlist made --unshare-net absent
// — keeping the sandbox in the host's network namespace and making a
// configured allowlist strictly WEAKER than no allowlist at all
// (GO-SEC4-002). See netns_bridge_linux.go for how a non-empty allowlist
// still reaches the host proxy without sharing the host netns.
func TestBwrapArgs_UnshareNetUnconditional(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed")
	}

	// Empty allowlist: --unshare-net present, as before.
	cmdEmpty := exec.Command("/bin/true")
	cleanupEmpty, isolatedEmpty, err := applyOSSandbox(cmdEmpty, t.TempDir(), "", nil, "")
	if err != nil {
		t.Fatalf("applyOSSandbox (empty): %v", err)
	}
	defer cleanupEmpty()
	if !isolatedEmpty {
		t.Error("applyOSSandbox (empty allowlist, bwrap present): isolated = false, want true")
	}
	emptyArgs := strings.Join(cmdEmpty.Args, " ")
	if !strings.Contains(emptyArgs, "--unshare-net") {
		t.Errorf("empty networkAllow: --unshare-net missing; expected offline sandbox\nargs: %s", emptyArgs)
	}

	// Non-empty allowlist: --unshare-net must ALSO be present now (the
	// pre-AD-02 behavior gated it off here — that inversion is exactly
	// what GO-SEC4-002 flagged).
	proxyLn, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for fake proxy: %v", err)
	}
	defer proxyLn.Close()

	cmdAllow := exec.Command("/bin/true")
	cleanupAllow, isolatedAllow, err := applyOSSandbox(cmdAllow, t.TempDir(), "", []string{"example.com"}, proxyLn.Addr().String())
	if err != nil {
		t.Fatalf("applyOSSandbox (allowlist): %v", err)
	}
	defer cleanupAllow()
	if !isolatedAllow {
		t.Error("applyOSSandbox (non-empty allowlist, bwrap present): isolated = false, want true")
	}
	allowArgs := strings.Join(cmdAllow.Args, " ")
	if !strings.Contains(allowArgs, "--unshare-net") {
		t.Errorf("non-empty networkAllow: --unshare-net MISSING — AD-02 regression (GO-SEC4-002: allowlist would again be weaker than no allowlist)\nargs: %s", allowArgs)
	}
	// The payload should now be the netns bridge trampoline, not the
	// original /bin/true directly — confirms the bridge actually engaged.
	if !strings.Contains(allowArgs, netnsHelperArg) {
		t.Errorf("non-empty networkAllow: bwrap payload does not route through the netns bridge trampoline (%s missing)\nargs: %s", netnsHelperArg, allowArgs)
	}
}

// TestApplyOSSandbox_FailsClosedWithoutBwrap is AD-01's headline
// regression test (TASKS/audit-remediation/ARCHITECT-DECISIONS.md,
// decided 2026-08-22): with bwrap unavailable and the opt-in unset,
// applyOSSandbox MUST return a non-nil error and isolated=false — never
// the pre-fix success shape `(func(){}, nil)`. That old two-return shape
// is provably unreachable now: it can't even compile against this
// function's current three-return signature, but this test also pins the
// runtime values.
//
// bwrap is made "unavailable" by restricting PATH to a directory that
// does not contain it, per the task file's own suggested technique — this
// runs regardless of whether the real build machine happens to have bwrap
// installed.
func TestApplyOSSandbox_FailsClosedWithoutBwrap(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv(allowUnsandboxedExecEnvVar, "")

	cmd := exec.Command("/bin/true")
	cleanup, isolated, err := applyOSSandbox(cmd, t.TempDir(), "", nil, "")
	if err == nil {
		t.Fatal("applyOSSandbox with bwrap unavailable and no opt-in: error = nil, want non-nil (fail closed)")
	}
	if isolated {
		t.Error("applyOSSandbox with bwrap unavailable and no opt-in: isolated = true, want false")
	}
	if cleanup != nil {
		t.Error("applyOSSandbox with bwrap unavailable and no opt-in: cleanup != nil, want nil (caller must not proceed)")
	}
	if !strings.Contains(err.Error(), allowUnsandboxedExecEnvVar) {
		t.Errorf("error %q does not name the opt-in env var as the remediation path", err.Error())
	}
}

// TestApplyOSSandbox_DegradesWithOptIn confirms the deliberate escape
// hatch works, and that it produces an OBSERVABLE degraded signal
// (isolated=false) rather than a silent success — the actual invariant
// AD-01 exists to guarantee, independent of which of the two outcomes
// (error vs. degrade) is chosen for a given call.
func TestApplyOSSandbox_DegradesWithOptIn(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv(allowUnsandboxedExecEnvVar, "1")

	cmd := exec.Command("/bin/true")
	cleanup, isolated, err := applyOSSandbox(cmd, t.TempDir(), "", nil, "")
	if err != nil {
		t.Fatalf("applyOSSandbox with bwrap unavailable and opt-in set: error = %v, want nil", err)
	}
	if cleanup == nil {
		t.Fatal("applyOSSandbox with bwrap unavailable and opt-in set: cleanup = nil, want a callable no-op")
	}
	cleanup()
	if isolated {
		t.Error("applyOSSandbox with bwrap unavailable and opt-in set: isolated = true, want false — no real isolation was applied, only the config knob was honored")
	}
	// The command itself must be left unwrapped (no bwrap re-exec) —
	// degraded mode really does run the original command directly.
	if cmd.Path != "/bin/true" && !strings.HasSuffix(cmd.Path, "/true") {
		t.Errorf("degraded mode rewrote cmd.Path to %q, want the original command untouched", cmd.Path)
	}
}

// TestNetnsBridge_ForwardsToHostProxy is AD-02's real integration test
// (TASKS/audit-remediation/ARCHITECT-DECISIONS.md; GO-SEC4-002): with a
// non-empty NetworkAllow, a command run through AgentExec must be able to
// reach the host-side allowlist Proxy via the HTTP_PROXY env var
// AgentExec already sets — end to end, through --unshare-net, the netns
// bridge trampoline, and back out to a real HTTP listener standing in for
// the Proxy's own upstream dial.
func TestNetnsBridge_ForwardsToHostProxy(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not installed")
	}

	t.Setenv("HOME", t.TempDir())

	// AgentExec starts its own real Proxy (proxy.go) when NetworkAllow is
	// non-empty; that Proxy allowlists "example.com" and forwards to
	// whatever example.com resolves to over the real network — not
	// controllable in a hermetic test. Instead this test exercises the
	// bridge mechanism directly: point curl, from inside the sandbox, at
	// 127.0.0.1:<forwardedPort> — the exact address/port AgentExec's own
	// HTTP_PROXY env var would carry — and confirm the connection reaches
	// a plain host-side TCP listener through the bridge, proving the
	// namespace-crossing relay itself works, independent of Proxy's own
	// allowlist/SSRF logic (already covered by proxy_test.go).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("bridged-ok"))
	}))
	defer srv.Close()

	srvURL, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse httptest URL: %v", err)
	}
	proxyAddr := srvURL.Host // "127.0.0.1:<port>" — stands in for Proxy.Addr

	sandboxDir := t.TempDir()
	cmd := exec.Command("curl", "-s", "-m", "5", "http://"+proxyAddr+"/")
	cleanup, isolated, err := applyOSSandbox(cmd, sandboxDir, "", []string{"example.com"}, proxyAddr)
	if err != nil {
		t.Fatalf("applyOSSandbox: %v", err)
	}
	defer cleanup()
	if !isolated {
		t.Fatal("applyOSSandbox with bwrap present: isolated = false, want true")
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bridged curl failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "bridged-ok") {
		t.Fatalf("bridged curl output = %q, want it to contain the host server's response", out)
	}
}

// TestNetnsBridge_HostArbitraryPortStillBlocked confirms namespace
// isolation still holds even with the bridge wired in: a port on the
// HOST's loopback that was NOT forwarded (i.e. not proxyAddr) must remain
// unreachable from inside the sandbox. This is the other half of
// GO-SEC4-002's fix — the sandboxed process gains a route to exactly the
// allowlist proxy's port, not general host-loopback access.
func TestNetnsBridge_HostArbitraryPortStillBlocked(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not installed")
	}
	t.Setenv("HOME", t.TempDir())

	// The "real" forwarded proxy (unused by this test's curl call).
	forwarded := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer forwarded.Close()
	forwardedURL, err := url.Parse(forwarded.URL)
	if err != nil {
		t.Fatalf("parse forwarded URL: %v", err)
	}

	// A second, NOT-forwarded host listener — the sandbox has no route to
	// this port even though it's also on 127.0.0.1.
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("should-not-be-reachable"))
	}))
	defer blocked.Close()
	blockedURL, err := url.Parse(blocked.URL)
	if err != nil {
		t.Fatalf("parse blocked URL: %v", err)
	}

	sandboxDir := t.TempDir()
	cmd := exec.Command("curl", "-s", "-m", "3", "http://"+blockedURL.Host+"/")
	cleanup, isolated, err := applyOSSandbox(cmd, sandboxDir, "", []string{"example.com"}, forwardedURL.Host)
	if err != nil {
		t.Fatalf("applyOSSandbox: %v", err)
	}
	defer cleanup()
	if !isolated {
		t.Fatal("applyOSSandbox with bwrap present: isolated = false, want true")
	}

	out, runErr := cmd.CombinedOutput()
	if runErr == nil && strings.Contains(string(out), "should-not-be-reachable") {
		t.Fatalf("non-forwarded host port was reachable from inside the sandbox — netns isolation regressed\noutput: %s", out)
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
	if !res.SandboxIsolated {
		t.Error("res.SandboxIsolated = false with bwrap present, want true")
	}
}

// TestAgentExec_SandboxIsolatedField_Degraded confirms the end-to-end
// AgentExec path surfaces AD-01's degraded signal on the returned
// ExecResult, not just inside applyOSSandbox's own return values.
func TestAgentExec_SandboxIsolatedField_Degraded(t *testing.T) {
	// AgentExec's own Command is an absolute path ("/bin/echo"), so
	// restricting PATH only affects applyOSSandbox's internal
	// exec.LookPath("bwrap") lookup, not whether /bin/echo itself runs.
	t.Setenv("PATH", t.TempDir())
	// Ensure bwrap really is unreachable via this constrained PATH,
	// otherwise this test would silently pass for the wrong reason.
	if _, err := exec.LookPath("bwrap"); err == nil {
		t.Skip("bwrap unexpectedly resolvable on constrained PATH; cannot exercise the degraded path")
	}
	t.Setenv(allowUnsandboxedExecEnvVar, "1")
	t.Setenv("HOME", t.TempDir())

	res, err := AgentExec(AgentExecOpts{
		SessionID: "test-degraded-field",
		Command:   "/bin/echo",
		Args:      []string{"hi"},
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("AgentExec (degraded, opted in): %v", err)
	}
	if res.SandboxIsolated {
		t.Error("res.SandboxIsolated = true in degraded mode, want false")
	}
}
