package sandbox

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// allowUnsandboxedExecEnvVar is the operator opt-in that permits
// AgentExec/UserExec to proceed WITHOUT OS-level isolation when the
// platform's isolation tool is unavailable (bwrap missing on Linux; no
// OS-level sandbox at all on non-darwin/non-linux platforms).
//
// Per AD-01 (TASKS/audit-remediation/ARCHITECT-DECISIONS.md, decided
// 2026-08-22): the default is fail closed — a missing isolation tool is a
// hard error. This env var is the explicit, deliberate escape hatch; it
// existed nowhere in this codebase before this change (grep-confirmed at
// decision time). Setting it re-creates the exact scenario in which
// CheckDenylist (denylist.go) is the ONLY remaining control — see that
// file's doc comment for GO-SEC4-006.
const allowUnsandboxedExecEnvVar = "NANITE_ALLOW_UNSANDBOXED_AGENT_EXEC"

// allowUnsandboxedExec reports whether the operator has opted in to
// degraded (no OS-level isolation) execution via allowUnsandboxedExecEnvVar.
// Accepts "1"/"true"/"yes" (case-insensitive, trimmed); anything else,
// including unset, is false — fail closed is the default.
func allowUnsandboxedExec() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(allowUnsandboxedExecEnvVar)))
	switch v {
	case "1", "true", "yes":
		return true
	}
	return false
}

// resolveIsolationVerdict is AD-01's decision function, factored out of
// os_linux.go/os_other.go so the actual policy (fail closed by default;
// degrade only on explicit opt-in; never silently succeed either way) is
// testable in isolation on any platform, without needing a real bwrap
// binary, a real missing-sandbox platform, or a linux/other build tag.
//
//   - available == true: real OS-level isolation was applied. Always
//     (isolated=true, "", nil) — the opt-in is irrelevant here.
//   - available == false, opt-in unset: fail closed. Returns a non-nil
//     error naming the remediation (install the tool, or set the opt-in)
//     and isolated=false. The caller must not run the command at all.
//   - available == false, opt-in set: degrade. Returns isolated=false,
//     err=nil, and a non-empty warnMsg the caller MUST log — every call,
//     not once per process lifetime. (The pre-AD-01 sync.Once behavior,
//     where this warning fired once and then every subsequent unsandboxed
//     exec was completely silent, was the actual mechanism of
//     GO-SEC4-001. That's why this function returns the message instead
//     of logging it itself behind a package-level guard — the caller logs
//     unconditionally, every time, via logDegraded below.)
func resolveIsolationVerdict(available bool, unavailReason string) (isolated bool, warnMsg string, err error) {
	if available {
		return true, "", nil
	}
	if !allowUnsandboxedExec() {
		return false, "", fmt.Errorf(
			"sandbox: %s — OS-level isolation is required; install it, or set %s=1 to explicitly accept unisolated execution (not recommended: the command denylist becomes the only remaining control, see GO-SEC4-006)",
			unavailReason, allowUnsandboxedExecEnvVar,
		)
	}
	return false, fmt.Sprintf(
		"sandbox: %s — running WITHOUT OS-level isolation (degraded mode explicitly enabled via %s=1)",
		unavailReason, allowUnsandboxedExecEnvVar,
	), nil
}

// logDegraded logs a degraded-execution warning. Called unconditionally on
// every degraded exec — deliberately NOT gated behind a sync.Once (that was
// GO-SEC4-001's actual mechanism: a long-running `nanite serve` process
// logged the warning once, then every subsequent unsandboxed exec for the
// rest of the process's lifetime was silent).
func logDegraded(msg string) {
	slog.Warn(msg)
}
