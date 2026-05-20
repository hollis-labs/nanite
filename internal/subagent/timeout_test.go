package subagent

import (
	"context"
	"strconv"
	"testing"
)

// TestTimeoutInRange pins the Torque-parity validation window
// [minTimeoutSeconds, maxTimeoutSeconds] (60s–7200s) used by both the
// env-var resolver and the explicit per-call timeout clamp in Spawn.
func TestTimeoutInRange(t *testing.T) {
	cases := []struct {
		secs int
		want bool
	}{
		{0, false},
		{-1, false},
		{59, false},
		{minTimeoutSeconds, true},   // 60 — lower bound inclusive
		{300, true},                 // the legacy fixed default
		{1800, true},                // DefaultTimeoutSeconds
		{maxTimeoutSeconds, true},    // 7200 — upper bound inclusive
		{maxTimeoutSeconds + 1, false},
		{999999, false},
	}
	for _, c := range cases {
		if got := timeoutInRange(c.secs); got != c.want {
			t.Errorf("timeoutInRange(%d) = %v, want %v", c.secs, got, c.want)
		}
	}
}

// TestResolveDefaultTimeoutSeconds_EnvUnset confirms the compiled-in
// DefaultTimeoutSeconds floor is used when the operator override env var
// is absent.
func TestResolveDefaultTimeoutSeconds_EnvUnset(t *testing.T) {
	t.Setenv(defaultTimeoutEnvVar, "")
	if got := resolveDefaultTimeoutSeconds(); got != DefaultTimeoutSeconds {
		t.Fatalf("resolveDefaultTimeoutSeconds() with env unset = %d, want %d (DefaultTimeoutSeconds)",
			got, DefaultTimeoutSeconds)
	}
}

// TestResolveDefaultTimeoutSeconds_EnvInRange confirms an operator can
// raise or lower the wall-clock backstop budget via the env var without
// recompiling — priority tier 1 of the Torque resolveTimeout mirror.
func TestResolveDefaultTimeoutSeconds_EnvInRange(t *testing.T) {
	for _, secs := range []int{minTimeoutSeconds, 600, 3600, maxTimeoutSeconds} {
		t.Run(strconv.Itoa(secs), func(t *testing.T) {
			t.Setenv(defaultTimeoutEnvVar, strconv.Itoa(secs))
			if got := resolveDefaultTimeoutSeconds(); got != secs {
				t.Fatalf("resolveDefaultTimeoutSeconds() = %d, want %d (env override)", got, secs)
			}
		})
	}
}

// TestResolveDefaultTimeoutSeconds_EnvOutOfRange confirms a typo'd env
// value (too small, too large, or non-integer) is ignored and the
// backstop falls back to DefaultTimeoutSeconds rather than being
// silently disabled or set to an unsafe value.
func TestResolveDefaultTimeoutSeconds_EnvOutOfRange(t *testing.T) {
	for _, raw := range []string{"0", "10", "59", "7201", "999999", "-100", "abc", "30s", "60.5"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv(defaultTimeoutEnvVar, raw)
			if got := resolveDefaultTimeoutSeconds(); got != DefaultTimeoutSeconds {
				t.Fatalf("resolveDefaultTimeoutSeconds() with bad env %q = %d, want %d (fallback)",
					raw, got, DefaultTimeoutSeconds)
			}
		})
	}
}

// TestSpawn_TimeoutResolution exercises the full priority order applied
// inside Spawn (CW-20260517-0036): an explicit in-range per-call timeout
// wins; an explicit out-of-range value is rejected and falls through to
// the env override; an unconfigured run lands on the env value or the
// compiled-in default. The persisted subagent_runs.timeout_seconds is
// the observable.
func TestSpawn_TimeoutResolution(t *testing.T) {
	cases := []struct {
		name        string
		envValue    string // "" = env unset
		reqTimeout  int
		wantTimeout int
	}{
		{"explicit in-range wins over env", "600", 1200, 1200},
		{"explicit out-of-range rejected, env used", "600", 5, 600},
		{"explicit too-large rejected, env used", "600", 999999, 600},
		{"unset request uses env override", "900", 0, 900},
		{"unset request, no env, uses default", "", 0, DefaultTimeoutSeconds},
		{"out-of-range env ignored, default used", "10", 0, DefaultTimeoutSeconds},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(defaultTimeoutEnvVar, c.envValue)
			db, _ := newTestDB(t)
			svc := NewService(db, EchoRunner{}, &stubPoster{}, nil, stubSettings{})

			id, err := svc.Spawn(context.Background(), SpawnRequest{
				ParentSessionID: "sess-1",
				ParentAgentID:   "file-backend",
				Role:            "file-summarizer",
				Prompt:          "summarize the repo",
				Mode:            ModeSync,
				TimeoutSeconds:  c.reqTimeout,
			})
			if err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			run, err := svc.Status(context.Background(), id)
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			if run.TimeoutSeconds != c.wantTimeout {
				t.Errorf("persisted timeout_seconds = %d, want %d",
					run.TimeoutSeconds, c.wantTimeout)
			}
		})
	}
}
