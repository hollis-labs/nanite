//go:build !(darwin || linux)

package sandbox

import (
	"fmt"
	"os/exec"
	"runtime"
)

// applyOSSandbox is a no-op on non-darwin/non-linux platforms — there is
// no OS-level sandbox mechanism on these platforms at all (unlike Linux,
// where the mechanism exists but its dependency, bwrap, might simply be
// missing). extraWritePath, networkAllow, and proxyAddr are accepted for
// signature parity with the darwin/linux implementations and are
// intentionally ignored here — without OS sandbox enforcement there's no
// allow-write list or network bridge to wire up.
//
// AD-01 (TASKS/audit-remediation/ARCHITECT-DECISIONS.md, decided
// 2026-08-22) treats this file as the SAME class as os_linux.go's
// bwrap-missing case, not a separate one — same fail-open shape, same
// sync.Once (now deleted here too). By default this FAILS CLOSED: a
// non-nil error, isolated=false. An operator can explicitly opt in to the
// old degraded behavior via NANITE_ALLOW_UNSANDBOXED_AGENT_EXEC=1 (see
// degraded.go); every degraded exec then logs at warn, unconditionally —
// not once per process lifetime as before.
func applyOSSandbox(cmd *exec.Cmd, sandboxDir string, extraWritePath string, networkAllow []string, proxyAddr string) (cleanup func(), isolated bool, err error) {
	_ = extraWritePath
	_ = networkAllow
	_ = proxyAddr

	isolated, warnMsg, verdictErr := resolveIsolationVerdict(false, fmt.Sprintf("no OS-level sandbox exists on this platform (%s)", runtime.GOOS))
	if verdictErr != nil {
		return nil, false, verdictErr
	}
	logDegraded(warnMsg)
	return func() {}, false, nil
}
