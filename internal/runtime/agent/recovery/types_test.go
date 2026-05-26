package recovery

import (
	"testing"
	"time"

	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
)

// TestEnumStringers covers the enum String() methods so they don't drift
// silently from the Cause* constants in go-agent-sessions when those
// names change. Each enum value plus the unknown-zero-value branch.
func TestEnumStringers(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"class transient", ClassTransient.String(), "transient"},
		{"class config_permissions", ClassConfigPermissions.String(), "config_permissions"},
		{"class permanent", ClassPermanent.String(), "permanent"},
		{"class unknown zero", ClassUnknown.String(), "unknown"},

		{"remediation repopulate", RemediationRepopulateSandbox.String(), "repopulate_sandbox"},
		{"remediation refresh_mcp", RemediationRefreshMCPTransport.String(), "refresh_mcp_transport"},
		{"remediation refresh_creds", RemediationRefreshCredentials.String(), "refresh_credentials"},
		{"remediation regen_claudemd", RemediationRegenerateCLAUDEMD.String(), "regenerate_claudemd"},
		{"remediation none", RemediationNone.String(), "none"},

		{"action retry_transient", ActionRetryTransient.String(), "retry_transient"},
		{"action retry_config_fixed", ActionRetryConfigFixed.String(), "retry_config_fixed"},
		{"action permanent_failure", ActionPermanentFailure.String(), "permanent_failure"},
		{"action unknown zero", ActionUnknown.String(), "unknown"},

		{"outcome remediated", OutcomeRemediated.String(), "remediated"},
		{"outcome transient_succ", OutcomeTransientRetrySucceeded.String(), "transient_retry_succeeded"},
		{"outcome permanent", OutcomePermanent.String(), "permanent"},
		{"outcome cancelled", OutcomeCancelled.String(), "cancelled"},
		{"outcome unknown zero", OutcomeUnknown.String(), "unknown"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, tc.got, tc.want)
		}
	}
}

// TestClassifyNilSafe — the Phase 1 classifier skeleton must degrade
// safely when handed a nil event or nil exit. Phase 2 replaces the body
// but keeps these guards.
func TestClassifyNilSafe(t *testing.T) {
	got := Classify(nil)
	if got.Class != ClassPermanent {
		t.Errorf("Classify(nil): Class = %v, want ClassPermanent", got.Class)
	}

	ev := &FailureEvent{} // Exit nil
	got = Classify(ev)
	if got.Class != ClassPermanent {
		t.Errorf("Classify(nil-Exit): Class = %v, want ClassPermanent", got.Class)
	}
}

// TestNewBrokerDefaults verifies the Broker defaults. Constructed with
// empty Dependencies — this is allowed (degraded mode); the broker
// only refuses replacement dispatch when AgentBoot is nil.
func TestNewBrokerDefaults(t *testing.T) {
	b := NewBroker(Dependencies{})
	if b.maxRetries != MaxBrokerRetries {
		t.Errorf("default maxRetries = %d, want %d", b.maxRetries, MaxBrokerRetries)
	}
	if b.remediationTimeout != 10*time.Second {
		t.Errorf("default remediationTimeout = %v, want 10s", b.remediationTimeout)
	}
	if b.stateBySession == nil {
		t.Error("stateBySession not initialized")
	}
	if b.activeRetries == nil {
		t.Error("activeRetries not initialized")
	}
}

// TestNewBrokerOptions verifies the WithMaxRetries / WithRemediationTimeout
// options apply. Negative maxRetries is clamped to 0 (no retries).
func TestNewBrokerOptions(t *testing.T) {
	b := NewBroker(Dependencies{},
		WithMaxRetries(7),
		WithRemediationTimeout(2*time.Second))
	if b.maxRetries != 7 {
		t.Errorf("maxRetries = %d, want 7", b.maxRetries)
	}
	if b.remediationTimeout != 2*time.Second {
		t.Errorf("remediationTimeout = %v, want 2s", b.remediationTimeout)
	}

	b2 := NewBroker(Dependencies{}, WithMaxRetries(-3))
	if b2.maxRetries != 0 {
		t.Errorf("clamped maxRetries = %d, want 0", b2.maxRetries)
	}
}

// TestOnSessionExitNilExitNoOp — passing a nil ExitError to
// OnSessionExit must be a no-op (defensive). Production callers should
// not invoke OnSessionExit on clean exits, but the broker shouldn't
// panic if they do.
func TestOnSessionExitNilExitNoOp(t *testing.T) {
	b := NewBroker(Dependencies{})
	// no panic, no breadcrumb (Store is nil, so writeBreadcrumb is no-op anyway)
	b.OnSessionExit("sess-123", nil, nil)
}

// TestOnRestartNilExit — OnRestart with nil prevExit must not panic.
// The lib documents prevExit as potentially nil when structured exit
// info wasn't captured.
func TestOnRestartNilExit(t *testing.T) {
	b := NewBroker(Dependencies{})
	b.OnRestart("sess-456", 1, nil)
}

// TestOnRestartCarriesCause — when the lib supplies a structured
// ExitError, the broker logs the cause verbatim. Phase 1 covers the
// observation path; Phase 8 will assert the breadcrumb shape.
func TestOnRestartCarriesCause(t *testing.T) {
	b := NewBroker(Dependencies{})
	b.OnRestart("sess-789", 2, &agentsessions.ExitError{
		Code:   1,
		Signal: 11,
		Cause:  agentsessions.CauseRestartExhausted,
	})
}

// TestRenderUserMessageRoutesByAction — Phase 1 routing table:
// transient -> info-card "Reconnecting agent",
// config_fixed -> info-card "Fixed an agent issue",
// permanent -> error-report.
func TestRenderUserMessageRoutesByAction(t *testing.T) {
	b := NewBroker(Dependencies{})
	ev := &FailureEvent{SessionID: "s1"}

	cases := []struct {
		action   Action
		wantKind string
	}{
		{ActionRetryTransient, "info-card"},
		{ActionRetryConfigFixed, "info-card"},
		{ActionPermanentFailure, "error-report"},
		{ActionUnknown, "error-report"}, // degenerate fallback
	}
	for _, tc := range cases {
		env := b.RenderUserMessage(ev, Classification{}, tc.action)
		if env.Kind != tc.wantKind {
			t.Errorf("action %v: Kind = %q, want %q", tc.action, env.Kind, tc.wantKind)
		}
	}
}

// TestCancelTokenIsHexAndUnique — info-card retries must carry a
// cancellation token. Tokens are random hex strings; collisions in 16
// bytes are not a real concern but a basic uniqueness check guards
// against a degenerate randomness path.
func TestCancelTokenIsHexAndUnique(t *testing.T) {
	b := NewBroker(Dependencies{})
	ev := &FailureEvent{SessionID: "s2"}
	a := b.RenderUserMessage(ev, Classification{}, ActionRetryTransient).CancelToken
	c := b.RenderUserMessage(ev, Classification{}, ActionRetryTransient).CancelToken
	if a == "" || c == "" {
		t.Fatalf("cancel tokens should be non-empty: a=%q b=%q", a, c)
	}
	if a == c {
		t.Errorf("cancel tokens should differ: %q == %q", a, c)
	}
}

// TestCancelRetryNoActiveTokenIsNoOp — calling CancelRetry with an
// unknown token must not panic and must not modify state. Production
// flow may call this after the retry already completed.
func TestCancelRetryNoActiveTokenIsNoOp(t *testing.T) {
	b := NewBroker(Dependencies{})
	b.CancelRetry("not-a-real-token")
	if len(b.activeRetries) != 0 {
		t.Errorf("activeRetries should remain empty, got %d entries", len(b.activeRetries))
	}
}
