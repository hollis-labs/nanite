package broker

import (
	"strings"
	"testing"

	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
)

// TestRenderUserMessageActionRouting pins the (Action -> Kind) routing.
// Phase 1 covered the smoke case; Phase 5 also pins Severity, Title,
// CancelToken-presence, and the chat-loop-terminated branch for
// restart_exhausted.
func TestRenderUserMessageActionRouting(t *testing.T) {
	b := NewBroker(Dependencies{})

	cases := []struct {
		name          string
		ev            *FailureEvent
		c             Classification
		action        Action
		wantKind      string
		wantSeverity  string
		wantHasTitle  string
		wantHasCancel bool
	}{
		{
			name:          "transient -> info-card",
			ev:            &FailureEvent{SessionID: "s", Exit: &agentsessions.ExitError{Code: 1}},
			c:             Classification{Class: ClassTransient},
			action:        ActionRetryTransient,
			wantKind:      "info-card",
			wantSeverity:  "info",
			wantHasTitle:  "Reconnecting",
			wantHasCancel: true,
		},
		{
			name:          "config-fixed -> info-card",
			ev:            &FailureEvent{SessionID: "s", Exit: &agentsessions.ExitError{Code: 1}},
			c:             Classification{Class: ClassConfigPermissions, Remediation: RemediationRefreshMCPTransport},
			action:        ActionRetryConfigFixed,
			wantKind:      "info-card",
			wantSeverity:  "info",
			wantHasTitle:  "Fixed",
			wantHasCancel: true,
		},
		{
			name:          "permanent (default) -> error-report",
			ev:            &FailureEvent{SessionID: "s", Exit: &agentsessions.ExitError{Code: 127}},
			c:             Classification{Class: ClassPermanent, Reason: "exit code 127: agent binary not found on PATH"},
			action:        ActionPermanentFailure,
			wantKind:      "error-report",
			wantSeverity:  "error",
			wantHasTitle:  "Agent unavailable",
			wantHasCancel: false,
		},
		{
			name:          "permanent restart_exhausted -> chat-loop-terminated",
			ev:            &FailureEvent{SessionID: "s", Exit: &agentsessions.ExitError{Cause: agentsessions.CauseRestartExhausted}},
			c:             Classification{Class: ClassPermanent, Reason: "restart_exhausted: lib-level retries exhausted"},
			action:        ActionPermanentFailure,
			wantKind:      "chat-loop-terminated",
			wantSeverity:  "error",
			wantHasTitle:  "Agent stopped",
			wantHasCancel: false,
		},
		{
			name:         "unknown action -> error-report fallback",
			ev:           &FailureEvent{SessionID: "s"},
			c:            Classification{},
			action:       ActionUnknown,
			wantKind:     "error-report",
			wantSeverity: "error",
			wantHasTitle: "Recovery error",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := b.RenderUserMessage(tc.ev, tc.c, tc.action)
			if got.Kind != tc.wantKind {
				t.Errorf("Kind: got %q, want %q", got.Kind, tc.wantKind)
			}
			if got.Severity != tc.wantSeverity {
				t.Errorf("Severity: got %q, want %q", got.Severity, tc.wantSeverity)
			}
			if !strings.Contains(got.Title, tc.wantHasTitle) {
				t.Errorf("Title: %q does not contain %q", got.Title, tc.wantHasTitle)
			}
			if tc.wantHasCancel && got.CancelToken == "" {
				t.Errorf("expected CancelToken, got empty")
			}
			if !tc.wantHasCancel && got.CancelToken != "" {
				t.Errorf("expected no CancelToken, got %q", got.CancelToken)
			}
		})
	}
}

// TestTransientRetryMessageVariesByCause verifies the user-facing
// transient message specializes for idle_timeout / oom_kill and falls
// back to a generic message for other causes.
func TestTransientRetryMessageVariesByCause(t *testing.T) {
	cases := []struct {
		name        string
		cause       string
		wantSubstrs []string
	}{
		{"idle timeout", agentsessions.CauseIdleTimeout, []string{"idle"}},
		{"oom kill", agentsessions.CauseOOMKill, []string{"memory"}},
		{"generic", "", []string{"temporary error"}},
		{"signal kill (no cause)", "", []string{"temporary error"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := &FailureEvent{Exit: &agentsessions.ExitError{Cause: tc.cause}}
			got := transientRetryMessage(ev, Classification{})
			for _, s := range tc.wantSubstrs {
				if !strings.Contains(strings.ToLower(got), s) {
					t.Errorf("message %q does not contain %q", got, s)
				}
			}
		})
	}
}

// TestConfigFixedMessageReflectsRemediation verifies each Remediation
// produces the right user-friendly phrase via Description().
func TestConfigFixedMessageReflectsRemediation(t *testing.T) {
	cases := []struct {
		name        string
		remediation Remediation
		wantSubstr  string
	}{
		{"sandbox", RemediationRepopulateSandbox, "sandbox"},
		{"mcp", RemediationRefreshMCPTransport, "MCP"},
		{"creds", RemediationRefreshCredentials, "credentials"},
		{"claudemd", RemediationRegenerateCLAUDEMD, "instructions"},
		{"none", RemediationNone, "configuration issue"}, // fallback
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := configFixedMessage(Classification{Remediation: tc.remediation})
			if !strings.Contains(got, tc.wantSubstr) {
				t.Errorf("message %q does not contain %q", got, tc.wantSubstr)
			}
		})
	}
}

// TestPermanentDetailRendersClassifierReason verifies the permanent
// envelope body capitalizes + period-terminates the classifier reason
// (which is shaped for logs, not surface).
func TestPermanentDetailRendersClassifierReason(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty falls back", "", "The agent could not be recovered."},
		{"adds period", "exit code 127: agent binary not found", "Exit code 127: agent binary not found."},
		{"already has period", "code 127.", "Code 127."},
		{"trims whitespace", "  exit failed  ", "Exit failed."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := permanentDetail(nil, Classification{Reason: tc.in})
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPermanentSuggestedActionVariesByRemediation verifies the closing
// suggestion adapts to the underlying remediation type. Credentials
// failures suggest "check API credentials"; sandbox/CLAUDE.md
// failures suggest "restart the chat session"; default suggests
// "different agent".
func TestPermanentSuggestedActionVariesByRemediation(t *testing.T) {
	cases := []struct {
		name        string
		remediation Remediation
		wantSubstr  string
	}{
		{"creds", RemediationRefreshCredentials, "credentials"},
		{"sandbox", RemediationRepopulateSandbox, "restart"},
		{"claudemd", RemediationRegenerateCLAUDEMD, "restart"},
		{"none default", RemediationNone, "different agent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := permanentSuggestedAction(nil, Classification{Remediation: tc.remediation})
			if !strings.Contains(got, tc.wantSubstr) {
				t.Errorf("got %q, want substring %q", got, tc.wantSubstr)
			}
		})
	}
}

// TestRemediationDescription pins the user-facing phrase for every
// Remediation value plus the unknown fallback.
func TestRemediationDescription(t *testing.T) {
	cases := []struct {
		r    Remediation
		want string
	}{
		{RemediationRepopulateSandbox, "rebuilt the agent's sandbox directory"},
		{RemediationRefreshMCPTransport, "restarted the agent's MCP transport"},
		{RemediationRefreshCredentials, "refreshed agent credentials"},
		{RemediationRegenerateCLAUDEMD, "refreshed the agent's instructions"},
		{RemediationNone, "a configuration issue"},
		{Remediation(999), "a configuration issue"},
	}
	for _, tc := range cases {
		if got := tc.r.Description(); got != tc.want {
			t.Errorf("(%v).Description() = %q, want %q", tc.r, got, tc.want)
		}
	}
}

// TestCapitalize covers the tiny first-rune-capitalize helper.
func TestCapitalize(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"abc", "Abc"},
		{"Abc", "Abc"},
		{"ABC", "ABC"},
		{"éclair", "éclair"}, // ASCII-only helper: leading non-ASCII unchanged
		{"123", "123"},
	}
	for _, tc := range cases {
		if got := capitalize(tc.in); got != tc.want {
			t.Errorf("capitalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestCancelTokenIsHexAndUnique remains here for envelope-side coverage
// (Phase 1 had a smoke version; this version is more strict on hex shape).
func TestCancelTokenHexLength(t *testing.T) {
	tok := newCancelToken()
	if len(tok) != 32 {
		t.Errorf("token length = %d, want 32 (16 bytes hex-encoded)", len(tok))
	}
	for _, ch := range tok {
		isHex := (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')
		if !isHex {
			t.Errorf("token %q contains non-hex char %q", tok, ch)
			break
		}
	}
}
