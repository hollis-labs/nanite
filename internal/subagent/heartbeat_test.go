package subagent

import (
	"strconv"
	"testing"
	"time"

	core "github.com/hollis-labs/substrate/agent/subagent"
)

// TestHeartbeatIntervalInRange pins the validation window
// [core.MinHeartbeatSeconds, core.MaxHeartbeatSeconds] used by the env-var
// resolver. 0 is handled separately by resolveHeartbeatInterval as
// "disabled", not validated here.
func TestHeartbeatIntervalInRange(t *testing.T) {
	cases := []struct {
		secs int
		want bool
	}{
		{-1, false},
		{0, false},
		{core.MinHeartbeatSeconds, true},
		{30, true}, // core.DefaultHeartbeatSeconds
		{core.MaxHeartbeatSeconds, true},
		{core.MaxHeartbeatSeconds + 1, false},
		{999999, false},
	}
	for _, c := range cases {
		if got := core.HeartbeatIntervalInRange(c.secs); got != c.want {
			t.Errorf("core.HeartbeatIntervalInRange(%d) = %v, want %v", c.secs, got, c.want)
		}
	}
}

// TestResolveHeartbeatInterval_EnvUnset confirms the compiled-in
// core.DefaultHeartbeatSeconds cadence is used when the operator override
// env var is absent.
func TestResolveHeartbeatInterval_EnvUnset(t *testing.T) {
	t.Setenv(heartbeatSecondsEnvVar, "")
	if got, want := resolveHeartbeatInterval(), core.DefaultHeartbeatSeconds*time.Second; got != want {
		t.Fatalf("resolveHeartbeatInterval() with env unset = %v, want %v", got, want)
	}
}

// TestResolveHeartbeatInterval_EnvZeroDisables confirms an operator can
// turn heartbeats off entirely with an explicit "0" — distinct from an
// invalid/out-of-range value, which falls back to the default rather
// than disabling.
func TestResolveHeartbeatInterval_EnvZeroDisables(t *testing.T) {
	t.Setenv(heartbeatSecondsEnvVar, "0")
	if got := resolveHeartbeatInterval(); got != 0 {
		t.Fatalf("resolveHeartbeatInterval() with env=0 = %v, want 0 (disabled)", got)
	}
}

// TestResolveHeartbeatInterval_EnvInRange confirms an operator can
// retune the cadence via the env var without recompiling.
func TestResolveHeartbeatInterval_EnvInRange(t *testing.T) {
	for _, secs := range []int{core.MinHeartbeatSeconds, 10, 60, core.MaxHeartbeatSeconds} {
		t.Run(strconv.Itoa(secs), func(t *testing.T) {
			t.Setenv(heartbeatSecondsEnvVar, strconv.Itoa(secs))
			if got, want := resolveHeartbeatInterval(), time.Duration(secs)*time.Second; got != want {
				t.Fatalf("resolveHeartbeatInterval() = %v, want %v (env override)", got, want)
			}
		})
	}
}

// TestResolveHeartbeatInterval_EnvOutOfRange confirms a typo'd env value
// (too large, non-integer, or negative) is ignored and the cadence falls
// back to core.DefaultHeartbeatSeconds rather than being silently disabled or
// set to an unsafe value. Only "0" (tested above) disables.
func TestResolveHeartbeatInterval_EnvOutOfRange(t *testing.T) {
	for _, raw := range []string{"-1", "301", "999999", "abc", "30s", "1.5"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv(heartbeatSecondsEnvVar, raw)
			if got, want := resolveHeartbeatInterval(), core.DefaultHeartbeatSeconds*time.Second; got != want {
				t.Fatalf("resolveHeartbeatInterval() with bad env %q = %v, want %v (fallback)",
					raw, got, want)
			}
		})
	}
}

// TestExecute_EmitsHeartbeatsWhileRunnerInFlight is the core behavioral
// test for CW-20260519-0068: while a subagent's runner call is blocked
// (the exact "14 minutes, zero output" pathology from the ticket's
// evidence), the parent's stream sink should keep receiving periodic
// "still running" pings, not just the initial dispatch event and the
// eventual terminal event.
