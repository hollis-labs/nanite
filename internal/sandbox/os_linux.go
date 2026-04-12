//go:build linux

package sandbox

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

var bwrapWarnOnce sync.Once

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
// The returned cleanup function is a no-op since bwrap needs no temp files.
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
//   - The network namespace is always unshared. In proxy mode the
//     sandbox receives HTTP_PROXY / HTTPS_PROXY env vars pointing at the
//     loopback proxy; since the network ns is unshared, the proxy is
//     effectively unreachable from inside the sandbox without host
//     cooperation. The proxy-mode coverage on Linux is documented as a
//     beta gap (gap #3) — see docs/audits/2026-04-10-sandbox-hardening.
//   - --die-with-parent and --new-session prevent orphan escape and TTY
//     hijacking (gap #5 partial).
func applyOSSandbox(cmd *exec.Cmd, sandboxDir string, networkAllow []string) (cleanup func(), err error) {
	bwrapPath, lookErr := exec.LookPath("bwrap")
	if lookErr != nil {
		bwrapWarnOnce.Do(func() {
			slog.Warn("sandbox: bwrap not found — install bubblewrap for OS-level isolation (using Tier 1 only)")
		})
		return func() {}, nil
	}

	absDir, err := filepath.Abs(sandboxDir)
	if err != nil {
		return nil, fmt.Errorf("resolve sandbox dir: %w", err)
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

	bwrapArgs = append(bwrapArgs,
		"--bind", absDir, absDir, // writable sandbox directory
		"--tmpfs", "/tmp", //       per-invocation tmpfs, no host /tmp leakage
		"--dev", "/dev", //         minimal /dev
		"--proc", "/proc", //       /proc view (scoped by --unshare-pid)
	)

	// Namespace isolation. --unshare-user-try degrades on kernels that
	// disable unprivileged user namespaces (common in hardened distros);
	// the rest are always available. --unshare-net is always set: in
	// proxy mode the Linux coverage is intentionally constrained (see
	// audit finding 06 gap #3); callers that need host-network egress on
	// Linux should use macOS seatbelt enforcement or run unprivileged.
	bwrapArgs = append(bwrapArgs,
		"--unshare-pid",
		"--unshare-ipc",
		"--unshare-uts",
		"--unshare-cgroup-try",
		"--unshare-user-try",
		"--unshare-net",
		"--new-session",
		"--die-with-parent",
	)

	// networkAllow is accepted as a parameter for parity with the macOS
	// seatbelt path but Linux enforcement happens at the namespace level
	// above. Document the intent so the parameter does not read as dead.
	_ = networkAllow

	bwrapArgs = append(bwrapArgs, "--")

	// Append original command and its arguments.
	origPath := cmd.Path
	origArgs := cmd.Args[1:] // Args[0] is the command name

	bwrapArgs = append(bwrapArgs, origPath)
	bwrapArgs = append(bwrapArgs, origArgs...)

	cmd.Path = bwrapPath
	cmd.Args = bwrapArgs

	return func() {}, nil
}
