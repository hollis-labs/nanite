package broker

import (
	"strings"

	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
)

// CauseHTTPRequestRejected is supplied by the HTTP chat path after surfacing
// a request/configuration failure. Retrying the unchanged request cannot help.
const CauseHTTPRequestRejected = "http_request_rejected"

// Classify decides the broker's response to a FailureEvent. Pure: same
// input always produces same output. Per-session state (e.g. "is this
// the second occurrence?") flows via FailureEvent.Attempt — populated
// by the broker before calling Classify.
//
// Rule precedence (top wins):
//
//  1. Nil event or nil exit          → Permanent (defensive — caller bug)
//  2. Cause http_request_rejected    → Permanent (request must change)
//  3. Cause idle_timeout             → Transient (no remediation)
//  4. Cause watchdog_kill            → ConfigPermissions, RegenerateCLAUDEMD
//  5. Cause oom_kill                 → Transient (single retry)
//  6. Cause restart_exhausted        → Permanent
//  7. Cause resource_limit           → Permanent (won't help to retry the same workload)
//  8. Code 127 (command not found)   → Permanent
//  9. SandboxDirState.Missing        → ConfigPermissions, RepopulateSandbox
//  10. MCPTransport.Down              → ConfigPermissions, RefreshMCPTransport
//  11. Stderr ~ 401/403/unauthorized → ConfigPermissions, RefreshCredentials
//  12. Signal 11 (SEGV)              → Transient (single retry)
//  13. Signal 9 + SessionAge < 5s    → Permanent (likely missing binary / immediate config error)
//  14. Signal 9 + SessionAge >= 5s   → Transient
//  15. Default Code != 0:
//     Attempt == 1                → Transient
//     Attempt >= 2                → Permanent (avoid retry loops on
//     unclassified failures)
//
// The broker hard cap (Attempt >= broker.maxRetries) is enforced one
// layer up in the broker itself, before Classify is consulted. The
// classifier focuses on "what KIND of failure is this?"; the cap
// decision is "have we tried this enough times?".
func Classify(ev *FailureEvent) Classification {
	if ev == nil || ev.Exit == nil {
		return Classification{
			Class:       ClassPermanent,
			Reason:      "nil failure event or exit info",
			Remediation: RemediationNone,
		}
	}
	xe := ev.Exit

	// Cause-based: lib-supervisor-triggered terminations carry the
	// most authoritative classification signal, so they evaluate first.
	switch xe.Cause {
	case CauseHTTPRequestRejected:
		return Classification{
			Class:       ClassPermanent,
			Reason:      "provider rejected the request; change its settings or model before retrying",
			Remediation: RemediationNone,
		}
	case agentsessions.CauseIdleTimeout:
		return Classification{
			Class:       ClassTransient,
			Reason:      "idle_timeout: session went idle past supervisor threshold",
			Remediation: RemediationNone,
		}
	case agentsessions.CauseWatchdogKill:
		return Classification{
			Class:       ClassConfigPermissions,
			Reason:      "watchdog_kill: agent stuck — refresh CLAUDE.md to nudge",
			Remediation: RemediationRegenerateCLAUDEMD,
		}
	case agentsessions.CauseOOMKill:
		return Classification{
			Class:       ClassTransient,
			Reason:      "oom_kill: out-of-memory — single retry (workload may differ)",
			Remediation: RemediationNone,
		}
	case agentsessions.CauseRestartExhausted:
		return Classification{
			Class:       ClassPermanent,
			Reason:      "restart_exhausted: lib-level retries exhausted; broker escalates",
			Remediation: RemediationNone,
		}
	case agentsessions.CauseResourceLimit:
		return Classification{
			Class:       ClassPermanent,
			Reason:      "resource_limit: process exceeded configured cap; same workload will hit it again",
			Remediation: RemediationNone,
		}
	}

	// Code 127 = "command not found" from the shell wrap. The binary
	// the adapter expects isn't on PATH; retrying won't help.
	if xe.Code == 127 {
		return Classification{
			Class:       ClassPermanent,
			Reason:      "exit code 127: agent binary not found on PATH",
			Remediation: RemediationNone,
		}
	}

	// State-based remediations precede signal-based heuristics — if a
	// crash is rooted in missing sandbox content or a downed MCP
	// transport, that's the highest-leverage fix to attempt.
	if xe.Code != 0 && ev.SandboxDirState.Missing {
		return Classification{
			Class:       ClassConfigPermissions,
			Reason:      "sandbox dir missing — repopulate before retrying",
			Remediation: RemediationRepopulateSandbox,
		}
	}
	if xe.Code != 0 && ev.MCPTransport.Down {
		return Classification{
			Class:       ClassConfigPermissions,
			Reason:      "MCP transport down — restart before retrying",
			Remediation: RemediationRefreshMCPTransport,
		}
	}

	// Stderr-substring matches for auth failures. Case-insensitive so
	// "Unauthorized" / "401 unauthorized" / "HTTP 403" all hit.
	if isAuthFailure(ev.StderrTail) {
		return Classification{
			Class:       ClassConfigPermissions,
			Reason:      "stderr indicates auth failure (401/403/unauthorized)",
			Remediation: RemediationRefreshCredentials,
		}
	}

	// Signal-based heuristics. SIGSEGV (11) is exotic enough on a
	// stable claude binary that one retry is reasonable.
	if xe.Signal == 11 {
		return Classification{
			Class:       ClassTransient,
			Reason:      "SIGSEGV (signal 11): exotic crash, single retry",
			Remediation: RemediationNone,
		}
	}

	// SIGKILL (9) at very early ages is almost always a config error
	// (missing binary that bash exec'd anyway, sandbox refused exec,
	// etc.) — retrying won't help. Past the 5s threshold it's likelier
	// to be an external kill, where one retry is reasonable.
	const youngSessionThreshold = 5 // seconds
	if xe.Signal == 9 {
		if ev.SessionAge.Seconds() < float64(youngSessionThreshold) {
			return Classification{
				Class:       ClassPermanent,
				Reason:      "SIGKILL within 5s of start: likely config error, retry won't help",
				Remediation: RemediationNone,
			}
		}
		return Classification{
			Class:       ClassTransient,
			Reason:      "SIGKILL after steady-state: external kill, single retry",
			Remediation: RemediationNone,
		}
	}

	// Default: Code != 0 with no specific signal. First occurrence is
	// transient; subsequent occurrences escalate to avoid retry loops
	// on unclassified failures.
	if xe.Code != 0 {
		if ev.Attempt >= 2 {
			return Classification{
				Class:       ClassPermanent,
				Reason:      "unclassified non-zero exit on retry: escalating",
				Remediation: RemediationNone,
			}
		}
		return Classification{
			Class:       ClassTransient,
			Reason:      "unclassified non-zero exit, first occurrence: single retry",
			Remediation: RemediationNone,
		}
	}

	// xe.Code == 0 and no Cause — clean exit with a non-nil ExitError
	// is rare but possible (signal 0 + non-zero ProcessState bits, or
	// transient buildExitError quirks). Treat as not-our-problem.
	return Classification{
		Class:       ClassPermanent,
		Reason:      "exit code 0 with no supervisor cause — not a recovery candidate",
		Remediation: RemediationNone,
	}
}

// isAuthFailure case-insensitively scans the stderr tail for the
// well-known auth-failure markers. Cheap (no regex) — three substring
// checks against an ASCII-lowered copy of the input.
func isAuthFailure(stderrTail string) bool {
	if stderrTail == "" {
		return false
	}
	lower := strings.ToLower(stderrTail)
	return strings.Contains(lower, "401") ||
		strings.Contains(lower, "403") ||
		strings.Contains(lower, "unauthorized")
}
