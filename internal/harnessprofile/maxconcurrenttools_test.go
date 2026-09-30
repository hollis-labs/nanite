package harnessprofile

import "testing"

// max_concurrent_tools (CW-20260929-0012) resolves through the same layers as
// the other knobs, records where it came from, and is clamped to the host range.
func TestMaxConcurrentTools_DefaultAndLayers(t *testing.T) {
	res := mustResolve(t, newReg(t, nil), Inputs{})
	if res.Values.MaxConcurrentTools != DefaultMaxConcurrentTools || DefaultMaxConcurrentTools != 8 {
		t.Errorf("default = %d, want 8", res.Values.MaxConcurrentTools)
	}
	if s := res.Sources["max_concurrent_tools"]; s.Layer != "computed" {
		t.Errorf("default source = %+v, want computed", s)
	}
	if got := res.Effective().Values["max_concurrent_tools"]; got != 8 {
		t.Errorf("recorded value = %v, want 8", got)
	}

	res = mustResolve(t, newReg(t, nil), Inputs{Launch: Layer{Harness: Knobs{MaxConcurrentTools: i(3)}}})
	if res.Values.MaxConcurrentTools != 3 || res.Sources["max_concurrent_tools"].Layer != "launch" {
		t.Errorf("launch layer: %d from %q", res.Values.MaxConcurrentTools, res.Sources["max_concurrent_tools"].Layer)
	}
}

func TestMaxConcurrentTools_EnvBeatsLaunch(t *testing.T) {
	getenv := func(k string) (string, bool) {
		if k == "NANITE_HARNESS_MAX_CONCURRENT_TOOLS" {
			return "2", true
		}
		return "", false
	}
	res := mustResolve(t, newReg(t, nil), Inputs{Getenv: getenv, Launch: Layer{Harness: Knobs{MaxConcurrentTools: i(6)}}})
	if res.Values.MaxConcurrentTools != 2 || res.Sources["max_concurrent_tools"].Layer != "env:NANITE_HARNESS_MAX_CONCURRENT_TOOLS" {
		t.Errorf("env: %d from %q", res.Values.MaxConcurrentTools, res.Sources["max_concurrent_tools"].Layer)
	}
}

// Zero would stall every batch, and an enormous value defeats the cap; both are
// moved into [1, 64] and the change is recorded.
func TestMaxConcurrentTools_Clamped(t *testing.T) {
	for _, tc := range []struct{ asked, want int }{{0, 1}, {1, 1}, {64, 64}, {5000, 64}} {
		res := mustResolve(t, newReg(t, nil), Inputs{Launch: Layer{Harness: Knobs{MaxConcurrentTools: i(tc.asked)}}})
		s := res.Sources["max_concurrent_tools"]
		if res.Values.MaxConcurrentTools != tc.want {
			t.Errorf("asked %d: got %d, want %d", tc.asked, res.Values.MaxConcurrentTools, tc.want)
		}
		if clamped := tc.asked != tc.want; s.Clamped != clamped {
			t.Errorf("asked %d: Clamped = %v, want %v", tc.asked, s.Clamped, clamped)
		}
	}
}

func TestMaxConcurrentTools_NegativeRejected(t *testing.T) {
	neg := -2
	if err := validateLayer(Layer{Harness: Knobs{MaxConcurrentTools: &neg}}); err == nil {
		t.Error("negative max_concurrent_tools accepted")
	}
}
