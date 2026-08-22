package sandbox

import (
	"strings"
	"testing"
)

// TestAllowUnsandboxedExec_EnvParsing locks in AD-01's opt-in parsing:
// default (unset, or any value other than the accepted truthy forms) is
// false — fail closed is the default, not something a typo can silently
// widen into "allow".
func TestAllowUnsandboxedExec_EnvParsing(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"0", false},
		{"false", false},
		{"no", false},
		{"unset-nonsense", false},
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"  true  ", true},
		{"yes", true},
		{"YES", true},
	}
	for _, tc := range cases {
		t.Run("value="+tc.value, func(t *testing.T) {
			t.Setenv(allowUnsandboxedExecEnvVar, tc.value)
			if got := allowUnsandboxedExec(); got != tc.want {
				t.Errorf("allowUnsandboxedExec() with %s=%q = %v, want %v", allowUnsandboxedExecEnvVar, tc.value, got, tc.want)
			}
		})
	}
}

// TestAllowUnsandboxedExec_UnsetDefaultsFalse confirms the true default
// (env var never set at all, not even to "") is fail-closed.
func TestAllowUnsandboxedExec_UnsetDefaultsFalse(t *testing.T) {
	// t.Setenv guarantees isolation/restoration; explicitly unset for this
	// test rather than relying on ambient environment state.
	t.Setenv(allowUnsandboxedExecEnvVar, "")
	if allowUnsandboxedExec() {
		t.Error("allowUnsandboxedExec() = true with the env var empty, want false (fail closed by default)")
	}
}

// TestResolveIsolationVerdict_Available covers the "tool present" case:
// isolated=true, no warning, no error, and the opt-in state is irrelevant
// (it must NOT matter whether the operator has additionally set the
// opt-in — real isolation always wins over a config knob that only ever
// matters when isolation is unavailable).
func TestResolveIsolationVerdict_Available(t *testing.T) {
	for _, optIn := range []string{"", "1"} {
		t.Setenv(allowUnsandboxedExecEnvVar, optIn)
		isolated, warn, err := resolveIsolationVerdict(true, "irrelevant when available")
		if err != nil {
			t.Fatalf("resolveIsolationVerdict(available=true) error = %v, want nil", err)
		}
		if !isolated {
			t.Error("resolveIsolationVerdict(available=true) isolated = false, want true")
		}
		if warn != "" {
			t.Errorf("resolveIsolationVerdict(available=true) warn = %q, want empty", warn)
		}
	}
}

// TestResolveIsolationVerdict_UnavailableNoOptIn is AD-01's core, headline
// assertion: the default (opt-in NOT set) is a hard error, not a silent
// success. This is the regression guard for GO-SEC4-001 — the previous
// behavior this replaces was `(func(){}, nil)`: no error, no way to tell
// isolation didn't happen. That two-value shape can no longer even compile
// against this function's three-value signature, but this test also pins
// the runtime behavior: isolated must be false AND err must be non-nil.
func TestResolveIsolationVerdict_UnavailableNoOptIn(t *testing.T) {
	t.Setenv(allowUnsandboxedExecEnvVar, "")
	isolated, warn, err := resolveIsolationVerdict(false, "bwrap not found")
	if err == nil {
		t.Fatal("resolveIsolationVerdict(available=false, no opt-in) error = nil, want non-nil (fail closed)")
	}
	if isolated {
		t.Error("resolveIsolationVerdict(available=false, no opt-in) isolated = true, want false")
	}
	if warn != "" {
		t.Errorf("resolveIsolationVerdict(available=false, no opt-in) warn = %q, want empty (nothing to log — the caller never runs the command)", warn)
	}
	// The error should name the actual remediation paths so an operator
	// (or an agent reading a tool-error string) isn't left guessing.
	if !strings.Contains(err.Error(), "bwrap not found") {
		t.Errorf("error %q does not mention the unavailability reason", err.Error())
	}
	if !strings.Contains(err.Error(), allowUnsandboxedExecEnvVar) {
		t.Errorf("error %q does not name the opt-in env var as a remediation path", err.Error())
	}
}

// TestResolveIsolationVerdict_UnavailableWithOptIn is the deliberate
// escape hatch: isolated=false (still — no real isolation happened), err
// is nil (the caller is allowed to proceed), and a non-empty warning
// message is returned for the caller to log UNCONDITIONALLY (see
// logDegraded's own doc comment on why this must never be gated behind a
// sync.Once again).
func TestResolveIsolationVerdict_UnavailableWithOptIn(t *testing.T) {
	t.Setenv(allowUnsandboxedExecEnvVar, "1")
	isolated, warn, err := resolveIsolationVerdict(false, "bwrap not found")
	if err != nil {
		t.Fatalf("resolveIsolationVerdict(available=false, opt-in set) error = %v, want nil", err)
	}
	if isolated {
		t.Error("resolveIsolationVerdict(available=false, opt-in set) isolated = true, want false — no real isolation was applied")
	}
	if warn == "" {
		t.Fatal("resolveIsolationVerdict(available=false, opt-in set) warn = \"\", want a non-empty message the caller must log every time")
	}
	if !strings.Contains(warn, "bwrap not found") {
		t.Errorf("warn %q does not mention the unavailability reason", warn)
	}
	if !strings.Contains(warn, allowUnsandboxedExecEnvVar) {
		t.Errorf("warn %q does not name the opt-in that caused this degraded run", warn)
	}
}
