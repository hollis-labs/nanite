//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// bwrapRoBindCandidates is the narrowed set of host paths the sandboxed
// process needs read-only access to in order to run common script
// interpreters and resolve TLS / DNS. We deliberately do NOT bind /home,
// /root, /var, /srv, /opt, or arbitrary dotfiles — those can contain
// secrets (AWS creds, SSH keys, shell history) the agent must not see.
// Paths that don't exist on a given host are silently skipped so the
// bwrap invocation doesn't error on e.g. /lib64 on pure-multiarch
// systems.
var bwrapRoBindCandidates = []string{
	"/usr",
	"/lib",
	"/lib32",
	"/lib64",
	"/bin",
	"/sbin",
	"/etc/alternatives",
	"/etc/ssl",
	"/etc/ca-certificates",
	"/etc/resolv.conf",
	"/etc/hosts",
	"/etc/nsswitch.conf",
}

// applyOSSandbox wraps the command with Linux bubblewrap (bwrap) for OS-level isolation.
// The original command becomes an argument to bwrap.
// The returned cleanup function tears down any per-invocation resources
// (the AD-02 netns bridge, when one was wired in); it is a no-op otherwise.
//
// AD-01 (TASKS/audit-remediation/ARCHITECT-DECISIONS.md, decided
// 2026-08-22): when bwrap is not found, this function FAILS CLOSED — it
// returns a non-nil error and isolated=false, instead of the pre-fix
// behavior of silently falling back to Tier 1 (convention-level) isolation
// while still reporting success. An operator can explicitly opt in to the
// old degraded behavior via NANITE_ALLOW_UNSANDBOXED_AGENT_EXEC=1 (see
// degraded.go); every degraded exec then logs at warn, unconditionally —
// the previous sync.Once (fired once per process lifetime, then silent
// forever after) was the actual mechanism of GO-SEC4-001 and is gone.
//
// Hardening posture:
//
//   - Read-only mounts are narrowed to the interpreter / TLS paths in
//     bwrapRoBindCandidates rather than a blanket `--ro-bind / /`. This
//     closes the host-secrets read path flagged by the 2026-04-10 audit
//     (finding 06 gap #1): user dotfiles, SSH keys, and cloud creds are no
//     longer visible from inside the sandbox.
//   - PID, IPC, UTS, cgroup, and user namespaces are always unshared so
//     the sandboxed process cannot observe or interfere with host
//     processes (gap #2). `--unshare-user-try` degrades gracefully on
//     kernels that disable unprivileged user namespaces.
//   - /tmp is replaced with a per-invocation tmpfs (gap #4) so there is
//     no cross-session state leakage through shared /tmp files.
//   - --unshare-net is now UNCONDITIONAL (AD-02, decided 2026-08-22 — see
//     TASKS/audit-remediation/ARCHITECT-DECISIONS.md). Before this fix,
//     it was applied only `if len(networkAllow) == 0`, which meant
//     configuring an allowlist made the sandbox strictly WEAKER than
//     configuring nothing (the process kept the host's network namespace
//     and enforcement fell back to HTTP(S)_PROXY convention, ignorable by
//     any raw socket). When networkAllow is non-empty, this file's
//     newNetnsBridge (netns_bridge_linux.go) gives the sandboxed process a
//     way to reach the still-unmodified, still-host-netns allowlist Proxy
//     anyway: a relay that crosses the namespace boundary carries only
//     the same HTTP(S)-proxy-shaped byte stream Proxy already mediates,
//     never raw sandbox-side network access.
//   - --die-with-parent and --new-session prevent orphan escape and TTY
//     hijacking (gap #5 partial).
func applyOSSandbox(cmd *exec.Cmd, sandboxDir string, extraWritePath string, networkAllow []string, proxyAddr string) (cleanup func(), isolated bool, err error) {
	bwrapPath, lookErr := exec.LookPath("bwrap")
	isolated, warnMsg, verdictErr := resolveIsolationVerdict(lookErr == nil, "bwrap not found — install bubblewrap for OS-level isolation")
	if verdictErr != nil {
		return nil, false, verdictErr
	}
	if !isolated {
		logDegraded(warnMsg)
		return func() {}, false, nil
	}

	absDir, err := filepath.Abs(sandboxDir)
	if err != nil {
		return nil, false, fmt.Errorf("resolve sandbox dir: %w", err)
	}

	// CW-20260504-0003: optional caller-supplied working directory. Bound
	// read+write inside the bwrap namespace alongside the legacy
	// sandboxDir bind so commands can operate on user-granted paths.
	// The upstream permission gate (e.g. dev_tools resolveAllowed) is
	// the authoritative allow check; this binding is the OS-level
	// enforcement boundary.
	var absExtra string
	if extraWritePath != "" {
		absExtra, err = filepath.Abs(extraWritePath)
		if err != nil {
			return nil, false, fmt.Errorf("resolve working dir: %w", err)
		}
	}

	// Build bwrap argument list.
	bwrapArgs := []string{
		"bwrap",
	}

	// Narrow read-only mounts: only bind the subsystems we actually need.
	// Missing paths are skipped silently (different distros expose
	// different subsets of /lib*, /etc/ca-certificates, etc.).
	for _, path := range bwrapRoBindCandidates {
		if _, statErr := os.Lstat(path); statErr == nil {
			bwrapArgs = append(bwrapArgs, "--ro-bind", path, path)
		}
	}

	// CW-linux-tmp-shadow: --tmpfs /tmp MUST be applied before the writable
	// binds below, not after. bwrap applies mounts in argument order; if
	// absDir (or absExtra) happens to be a subpath of /tmp — which it can
	// be in practice (e.g. any test whose $HOME is itself a t.TempDir(),
	// confirmed directly via this task's real-Linux verification: "Can't
	// chdir to <path>: No such file or directory" for a path that WAS
	// bind-mounted, because a later blanket --tmpfs /tmp silently shadowed
	// the earlier, more specific bind) — a --bind issued before --tmpfs
	// /tmp gets hidden the moment the tmpfs mount lands on top of it. This
	// was a real, pre-existing gap: production sandboxDir values are never
	// under /tmp in practice ($HOME + ".nanite/sandboxes/..."), so it never
	// fired outside a test-only HOME-under-/tmp setup, but the fix is
	// unconditional and correct regardless — the writable binds always win
	// now, independent of where they happen to sit relative to /tmp.
	bwrapArgs = append(bwrapArgs,
		"--tmpfs", "/tmp", //       per-invocation tmpfs, no host /tmp leakage
		"--dev", "/dev", //         minimal /dev
		"--proc", "/proc", //       /proc view (scoped by --unshare-pid)
		"--bind", absDir, absDir, // writable sandbox directory
	)
	if absExtra != "" && absExtra != absDir {
		// Bind the caller's working dir read+write inside the namespace
		// so the command can operate on it. Skipped if it equals
		// sandboxDir (avoid duplicate --bind, which bwrap would error on).
		bwrapArgs = append(bwrapArgs, "--bind", absExtra, absExtra)
	}

	// Namespace isolation. --unshare-user-try degrades on kernels that
	// disable unprivileged user namespaces (common in hardened distros);
	// the rest are always available.
	bwrapArgs = append(bwrapArgs,
		"--unshare-pid",
		"--unshare-ipc",
		"--unshare-uts",
		"--unshare-cgroup-try",
		"--unshare-user-try",
		"--new-session",
		"--die-with-parent",
		// AD-02: unconditional. See the doc comment above.
		"--unshare-net",
	)

	// CW-linux-chdir: bwrap does not inherit cmd.Dir (the calling process's
	// cwd at exec time) into the sandboxed process the way a plain exec
	// would — after its own mount-namespace setup, bwrap chdir()s to "/"
	// before exec'ing the payload unless told otherwise. Without this,
	// AgentExec's documented CWD contract (cmd.Dir = WorkingDir or the
	// sandbox scoping dir) silently did not hold on Linux — a pre-existing
	// gap discovered via this task's real-Linux verification (darwin's
	// seatbelt wrapper never resets cwd, so this never showed up there;
	// see TestAgentExec_CWDRestricted / TestAgentExec_EmptyWorkingDir_
	// FallsBackToSandboxDir).
	//
	// Deliberately using absExtra/absDir here — the already filepath.Abs
	// -cleaned forms actually passed to --bind above — rather than the
	// caller's raw cmd.Dir. bwrap's --chdir target must be the EXACT
	// string a --bind mounted; cmd.Dir (e.g. opts.WorkingDir verbatim,
	// with a trailing slash or a non-canonical form) can textually differ
	// from its own filepath.Abs()-cleaned counterpart even when both name
	// the same directory, and bwrap has no path-canonicalization step of
	// its own before chdir — confirmed directly: an uncleaned cmd.Dir
	// produced "bwrap: Can't chdir to <path>: No such file or directory"
	// even though the (differently-spelled) same directory was correctly
	// bound.
	chdirTarget := absDir
	if absExtra != "" {
		chdirTarget = absExtra
	}
	bwrapArgs = append(bwrapArgs, "--chdir", chdirTarget)

	origPath := cmd.Path
	origArgs := cmd.Args[1:] // Args[0] is the command name

	payloadPath := origPath
	payloadArgs := origArgs
	bridgeCleanup := func() {}

	if len(networkAllow) > 0 && proxyAddr != "" {
		bridge, bridgeErr := newNetnsBridge(proxyAddr, origPath, origArgs, cmd.Env)
		if bridgeErr != nil {
			return nil, false, fmt.Errorf("sandbox: wire network-allowlist bridge: %w", bridgeErr)
		}
		bwrapArgs = append(bwrapArgs, bridge.extraBwrapArgs()...)
		payloadPath = bridge.payloadPath
		payloadArgs = bridge.payloadArgs
		cmd.Env = bridge.env
		bridgeCleanup = bridge.Close
	}

	bwrapArgs = append(bwrapArgs, "--")
	bwrapArgs = append(bwrapArgs, payloadPath)
	bwrapArgs = append(bwrapArgs, payloadArgs...)

	cmd.Path = bwrapPath
	cmd.Args = bwrapArgs

	return bridgeCleanup, true, nil
}
