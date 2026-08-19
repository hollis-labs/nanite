package broker

import (
	"strings"
	"testing"
	"time"

	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
)

// TestClassifyTable exercises every rule in the classifier's documented
// precedence list. Each row pins (Class, Remediation) so a refactor
// that silently changes routing produces a localized failure rather
// than a downstream flake.
//
// Rule numbering matches the comment block in classifier.go.
func TestClassifyTable(t *testing.T) {
	cases := []struct {
		name            string
		event           *FailureEvent
		wantClass       Class
		wantRemediation Remediation
		wantReasonHas   string // optional substring; "" skips check
	}{
		// Rule 1 — defensive nil cases.
		{
			name:            "nil event",
			event:           nil,
			wantClass:       ClassPermanent,
			wantRemediation: RemediationNone,
			wantReasonHas:   "nil failure event",
		},
		{
			name:            "nil exit",
			event:           &FailureEvent{},
			wantClass:       ClassPermanent,
			wantRemediation: RemediationNone,
			wantReasonHas:   "nil failure event",
		},

		// Rule 2 — idle_timeout.
		{
			name: "cause idle_timeout",
			event: &FailureEvent{
				Exit: &agentsessions.ExitError{Cause: agentsessions.CauseIdleTimeout},
			},
			wantClass:       ClassTransient,
			wantRemediation: RemediationNone,
			wantReasonHas:   "idle_timeout",
		},

		// Rule 3 — watchdog_kill.
		{
			name: "cause watchdog_kill",
			event: &FailureEvent{
				Exit: &agentsessions.ExitError{Cause: agentsessions.CauseWatchdogKill},
			},
			wantClass:       ClassConfigPermissions,
			wantRemediation: RemediationRegenerateCLAUDEMD,
			wantReasonHas:   "watchdog_kill",
		},

		// Rule 4 — oom_kill.
		{
			name: "cause oom_kill",
			event: &FailureEvent{
				Exit: &agentsessions.ExitError{Cause: agentsessions.CauseOOMKill},
			},
			wantClass:       ClassTransient,
			wantRemediation: RemediationNone,
			wantReasonHas:   "oom_kill",
		},

		// Rule 5 — restart_exhausted.
		{
			name: "cause restart_exhausted",
			event: &FailureEvent{
				Exit: &agentsessions.ExitError{Cause: agentsessions.CauseRestartExhausted},
			},
			wantClass:       ClassPermanent,
			wantRemediation: RemediationNone,
			wantReasonHas:   "restart_exhausted",
		},

		// Rule 6 — resource_limit.
		{
			name: "cause resource_limit",
			event: &FailureEvent{
				Exit: &agentsessions.ExitError{Cause: agentsessions.CauseResourceLimit},
			},
			wantClass:       ClassPermanent,
			wantRemediation: RemediationNone,
			wantReasonHas:   "resource_limit",
		},

		// Rule 7 — exit code 127.
		{
			name: "code 127 binary missing",
			event: &FailureEvent{
				Exit: &agentsessions.ExitError{Code: 127},
			},
			wantClass:       ClassPermanent,
			wantRemediation: RemediationNone,
			wantReasonHas:   "127",
		},

		// Rule 8 — sandbox dir missing.
		{
			name: "sandbox missing with non-zero exit",
			event: &FailureEvent{
				Exit:            &agentsessions.ExitError{Code: 1},
				SandboxDirState: SandboxState{Missing: true},
			},
			wantClass:       ClassConfigPermissions,
			wantRemediation: RemediationRepopulateSandbox,
			wantReasonHas:   "sandbox",
		},
		{
			// Sandbox missing but exit was clean (code 0): not a fail.
			// Falls through to the "exit code 0" terminal branch.
			name: "sandbox missing with code 0 falls through",
			event: &FailureEvent{
				Exit:            &agentsessions.ExitError{Code: 0},
				SandboxDirState: SandboxState{Missing: true},
			},
			wantClass:       ClassPermanent,
			wantRemediation: RemediationNone,
			wantReasonHas:   "exit code 0",
		},

		// Rule 9 — MCP transport down.
		{
			name: "MCP transport down with non-zero exit",
			event: &FailureEvent{
				Exit:         &agentsessions.ExitError{Code: 2},
				MCPTransport: MCPState{Down: true},
			},
			wantClass:       ClassConfigPermissions,
			wantRemediation: RemediationRefreshMCPTransport,
			wantReasonHas:   "MCP",
		},

		// Rule 10 — stderr auth-failure markers.
		{
			name: "stderr 401",
			event: &FailureEvent{
				Exit:       &agentsessions.ExitError{Code: 1},
				StderrTail: "HTTP 401 Unauthorized — token rejected",
			},
			wantClass:       ClassConfigPermissions,
			wantRemediation: RemediationRefreshCredentials,
			wantReasonHas:   "auth",
		},
		{
			name: "stderr 403",
			event: &FailureEvent{
				Exit:       &agentsessions.ExitError{Code: 1},
				StderrTail: "received 403 forbidden response from API",
			},
			wantClass:       ClassConfigPermissions,
			wantRemediation: RemediationRefreshCredentials,
		},
		{
			name: "stderr Unauthorized casing",
			event: &FailureEvent{
				Exit:       &agentsessions.ExitError{Code: 1},
				StderrTail: "AUTHENTICATION FAILED: Unauthorized",
			},
			wantClass:       ClassConfigPermissions,
			wantRemediation: RemediationRefreshCredentials,
		},

		// Rule 11 — SIGSEGV.
		{
			name: "signal 11 SIGSEGV",
			event: &FailureEvent{
				Exit: &agentsessions.ExitError{Code: -1, Signal: 11},
			},
			wantClass:       ClassTransient,
			wantRemediation: RemediationNone,
			wantReasonHas:   "SIGSEGV",
		},

		// Rule 12 — SIGKILL with very young session.
		{
			name: "signal 9 within 5s",
			event: &FailureEvent{
				Exit:       &agentsessions.ExitError{Code: -1, Signal: 9, Killed: true},
				SessionAge: 2 * time.Second,
			},
			wantClass:       ClassPermanent,
			wantRemediation: RemediationNone,
			wantReasonHas:   "SIGKILL within 5s",
		},

		// Rule 13 — SIGKILL after steady-state.
		{
			name: "signal 9 after 5s",
			event: &FailureEvent{
				Exit:       &agentsessions.ExitError{Code: -1, Signal: 9, Killed: true},
				SessionAge: 60 * time.Second,
			},
			wantClass:       ClassTransient,
			wantRemediation: RemediationNone,
			wantReasonHas:   "external kill",
		},

		// Rule 14 — default Code != 0, first occurrence.
		{
			name: "default code 1 first attempt",
			event: &FailureEvent{
				Exit:    &agentsessions.ExitError{Code: 1},
				Attempt: 1,
			},
			wantClass:       ClassTransient,
			wantRemediation: RemediationNone,
			wantReasonHas:   "first occurrence",
		},

		// Rule 14 — default Code != 0, second occurrence.
		{
			name: "default code 1 second attempt",
			event: &FailureEvent{
				Exit:    &agentsessions.ExitError{Code: 1},
				Attempt: 2,
			},
			wantClass:       ClassPermanent,
			wantRemediation: RemediationNone,
			wantReasonHas:   "retry",
		},

		// Default code 0 + no cause — degenerate-but-defensive branch.
		{
			name: "code 0 with no cause",
			event: &FailureEvent{
				Exit: &agentsessions.ExitError{Code: 0},
			},
			wantClass:       ClassPermanent,
			wantRemediation: RemediationNone,
			wantReasonHas:   "exit code 0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.event)
			if got.Class != tc.wantClass {
				t.Errorf("Class: got %v, want %v (reason: %q)", got.Class, tc.wantClass, got.Reason)
			}
			if got.Remediation != tc.wantRemediation {
				t.Errorf("Remediation: got %v, want %v (reason: %q)", got.Remediation, tc.wantRemediation, got.Reason)
			}
			if tc.wantReasonHas != "" && !strings.Contains(got.Reason, tc.wantReasonHas) {
				t.Errorf("Reason: %q does not contain %q", got.Reason, tc.wantReasonHas)
			}
		})
	}
}

// TestClassifyPrecedence pins the ordering between rules where multiple
// match a single event. Documented order in classifier.go:
//   - Cause beats state (cause > sandbox/MCP > stderr > signals > default)
//   - State beats stderr (sandbox/MCP > stderr)
//   - Stderr beats signal (stderr > SIGSEGV)
//   - Code 127 beats stderr/state (Permanent > anything)
func TestClassifyPrecedence(t *testing.T) {
	t.Run("cause idle_timeout wins over sandbox missing", func(t *testing.T) {
		ev := &FailureEvent{
			Exit: &agentsessions.ExitError{
				Code:  -1,
				Cause: agentsessions.CauseIdleTimeout,
			},
			SandboxDirState: SandboxState{Missing: true},
		}
		got := Classify(ev)
		if got.Class != ClassTransient || got.Remediation != RemediationNone {
			t.Errorf("expected Transient/None for idle_timeout despite missing sandbox, got %v/%v", got.Class, got.Remediation)
		}
	})

	t.Run("code 127 wins over stderr 401", func(t *testing.T) {
		ev := &FailureEvent{
			Exit:       &agentsessions.ExitError{Code: 127},
			StderrTail: "HTTP 401 unauthorized",
		}
		got := Classify(ev)
		if got.Class != ClassPermanent || got.Remediation != RemediationNone {
			t.Errorf("expected Permanent/None for code 127 despite 401 stderr, got %v/%v", got.Class, got.Remediation)
		}
	})

	t.Run("sandbox missing wins over stderr 401", func(t *testing.T) {
		ev := &FailureEvent{
			Exit:            &agentsessions.ExitError{Code: 1},
			SandboxDirState: SandboxState{Missing: true},
			StderrTail:      "HTTP 401 unauthorized",
		}
		got := Classify(ev)
		if got.Class != ClassConfigPermissions || got.Remediation != RemediationRepopulateSandbox {
			t.Errorf("expected ConfigPermissions/RepopulateSandbox, got %v/%v", got.Class, got.Remediation)
		}
	})

	t.Run("stderr 401 wins over SIGSEGV", func(t *testing.T) {
		ev := &FailureEvent{
			Exit:       &agentsessions.ExitError{Code: -1, Signal: 11},
			StderrTail: "401 unauthorized",
		}
		got := Classify(ev)
		if got.Class != ClassConfigPermissions || got.Remediation != RemediationRefreshCredentials {
			t.Errorf("expected ConfigPermissions/RefreshCredentials, got %v/%v", got.Class, got.Remediation)
		}
	})

	t.Run("MCP down wins over signal 9", func(t *testing.T) {
		ev := &FailureEvent{
			Exit:         &agentsessions.ExitError{Code: -1, Signal: 9, Killed: true},
			MCPTransport: MCPState{Down: true},
			SessionAge:   30 * time.Second,
		}
		got := Classify(ev)
		if got.Class != ClassConfigPermissions || got.Remediation != RemediationRefreshMCPTransport {
			t.Errorf("expected ConfigPermissions/RefreshMCPTransport, got %v/%v", got.Class, got.Remediation)
		}
	})
}

// TestIsAuthFailure pins the case-insensitive substring matcher's
// behavior against representative stderr fragments.
func TestIsAuthFailure(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"agent crashed", false},
		{"401", true},
		{"HTTP 401 unauthorized", true},
		{"403 forbidden", true},
		{"Unauthorized", true},
		{"UNAUTHORIZED", true},
		{"unauthorized access denied", true},
		{"some 4012 nonsense", true}, // substring "401" hits — accept the false positive risk
	}
	for _, tc := range cases {
		if got := isAuthFailure(tc.in); got != tc.want {
			t.Errorf("isAuthFailure(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
