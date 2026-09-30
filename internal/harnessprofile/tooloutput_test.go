package harnessprofile

import (
	"testing"

	agentcontracts "github.com/hollis-labs/agent-contracts-leaf"
)

func vals(t *testing.T, in Inputs) Values {
	t.Helper()
	return mustResolve(t, newReg(t, nil), in).Values
}

func ptr64(v int64) *int64 { return &v }

// The default ceiling is a function of the window, with today's 24 KiB as the
// floor, so a model with no window information behaves as it always did.
func TestTurnCeilingDefaultScalesWithWindow(t *testing.T) {
	v := vals(t, Inputs{})
	for _, tc := range []struct {
		name            string
		window, remains int
		want            int
	}{
		{"unknown window", 0, -1, 24 * 1024},
		{"200K window is the historical value", 200_000, -1, 24 * 1024},
		{"1M window scales up", 1_000_000, -1, 120_000},
		{"huge window hits the host-side max", 100_000_000, -1, 512 * 1024},
		{"tiny window keeps the floor", 8_000, -1, 24 * 1024},
	} {
		if got := v.TurnCeiling(tc.window, tc.remains); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

// Remaining context limits the ceiling below the floor when the window is
// nearly full, but never below the remaining floor.
func TestTurnCeilingRemainingBudget(t *testing.T) {
	v := vals(t, Inputs{})
	for _, tc := range []struct {
		remaining int
		want      int
	}{
		{-1, 120_000},        // unknown: no limit from it
		{1_000_000, 120_000}, // plenty left: the window-scaled base
		{40_000, 40_000},     // 40K tokens = 160 KB x 25%
		{10_000, 10_000},     // 10K tokens = 40 KB x 25%
		{2_000, 4096},        // nearly full: the floor
		{0, 4096},            // nothing left: the floor, not zero
	} {
		if got := v.TurnCeiling(1_000_000, tc.remaining); got != tc.want {
			t.Errorf("remaining %d: got %d, want %d", tc.remaining, got, tc.want)
		}
	}
	if v.RemainingCap(-1) != -1 || v.RemainingCap(10_000) != 10_000 || v.RemainingCap(0) != 4096 {
		t.Errorf("RemainingCap = %d %d %d", v.RemainingCap(-1), v.RemainingCap(10_000), v.RemainingCap(0))
	}
}

func TestExplicitToolOutputBytes(t *testing.T) {
	r := newReg(t, map[string]string{"big.yaml": "name: big\nlimits: {tool_output_bytes: 300000}\n", "off.yaml": "name: off\nlimits: {tool_output_bytes: 0}\n"})
	res := mustResolve(t, r, Inputs{Profile: "big"})
	if got := res.Values.TurnCeiling(200_000, -1); got != 300_000 {
		t.Errorf("explicit ceiling replaces the scaled one: %d", got)
	}
	if res.Sources["tool_output_bytes"].Layer != "profile:big" {
		t.Errorf("source = %+v", res.Sources["tool_output_bytes"])
	}
	// The explicit value still gets the remaining-budget clamp.
	if got := res.Values.TurnCeiling(200_000, 10_000); got != 10_000 {
		t.Errorf("explicit value must honor the remaining-budget cap: %d", got)
	}
	// Zero means no cumulative ceiling.
	off := mustResolve(t, r, Inputs{Profile: "off"})
	if got := off.Values.TurnCeiling(200_000, 10_000); got != 0 {
		t.Errorf("zero = no ceiling, got %d", got)
	}
	// But the remaining-budget cap on a single result survives it.
	if off.Values.RemainingCap(10_000) != 10_000 {
		t.Errorf("remaining cap with the ceiling off = %d", off.Values.RemainingCap(10_000))
	}
}

func TestToolOutputBytesLayersSourcesAndClamps(t *testing.T) {
	r := newReg(t, map[string]string{"p.yaml": "name: p\nlimits: {tool_output_bytes: 100000}\n"})
	agent := Layer{Limits: limitsWith(ptr64(90000))}
	launch := Layer{Limits: limitsWith(ptr64(80000))}
	env := func(k string) (string, bool) {
		if k == "NANITE_HARNESS_TOOL_OUTPUT_BYTES" {
			return "70000", true
		}
		return "", false
	}
	steps := []struct {
		in     Inputs
		want   int64
		source string
	}{
		{Inputs{Profile: "p"}, 100000, "profile:p"},
		{Inputs{Profile: "p", Agent: agent}, 90000, "agent"},
		{Inputs{Profile: "p", Agent: agent, Launch: launch}, 80000, "launch"},
		{Inputs{Profile: "p", Agent: agent, Launch: launch, Getenv: env}, 70000, "env:NANITE_HARNESS_TOOL_OUTPUT_BYTES"},
	}
	for _, st := range steps {
		res := mustResolve(t, r, st.in)
		if *res.Values.ToolOutputBytes != st.want || res.Sources["tool_output_bytes"].Layer != st.source {
			t.Errorf("got %d from %q, want %d from %q", *res.Values.ToolOutputBytes, res.Sources["tool_output_bytes"].Layer, st.want, st.source)
		}
	}
	// Host maximum clamps the explicit value last and records the requester.
	huge := mustResolve(t, r, Inputs{Launch: Layer{Limits: limitsWith(ptr64(1 << 40))}})
	if *huge.Values.ToolOutputBytes != 64<<20 || !huge.Sources["tool_output_bytes"].Clamped || huge.Sources["tool_output_bytes"].Layer != "launch" {
		t.Errorf("huge: %d %+v", *huge.Values.ToolOutputBytes, huge.Sources["tool_output_bytes"])
	}
	tiny := mustResolve(t, r, Inputs{Launch: Layer{Limits: limitsWith(ptr64(5))}})
	if *tiny.Values.ToolOutputBytes != 1024 || !tiny.Sources["tool_output_bytes"].Clamped {
		t.Errorf("tiny: %d %+v", *tiny.Values.ToolOutputBytes, tiny.Sources["tool_output_bytes"])
	}
	// Unset: the computed layer, no explicit value.
	def := mustResolve(t, r, Inputs{})
	if def.Values.ToolOutputBytes != nil || def.Sources["tool_output_bytes"].Layer != "computed" {
		t.Errorf("default: %v %+v", def.Values.ToolOutputBytes, def.Sources["tool_output_bytes"])
	}
}

// The original variable keeps working as an environment layer; the new name
// wins when both are set; a legacy value that does not parse is ignored, as it
// always was.
func TestLegacyToolCeilingEnv(t *testing.T) {
	r := newReg(t, nil)
	get := func(pairs map[string]string) func(string) (string, bool) {
		return func(k string) (string, bool) { v, ok := pairs[k]; return v, ok }
	}
	res := mustResolve(t, r, Inputs{Getenv: get(map[string]string{"NANITE_TOOL_TURN_CEILING_BYTES": "50000"})})
	if *res.Values.ToolOutputBytes != 50000 || res.Sources["tool_output_bytes"].Layer != "env:NANITE_TOOL_TURN_CEILING_BYTES" {
		t.Errorf("legacy alone: %v %+v", res.Values.ToolOutputBytes, res.Sources["tool_output_bytes"])
	}
	res = mustResolve(t, r, Inputs{Getenv: get(map[string]string{"NANITE_TOOL_TURN_CEILING_BYTES": "50000", "NANITE_HARNESS_TOOL_OUTPUT_BYTES": "60000"})})
	if *res.Values.ToolOutputBytes != 60000 || res.Sources["tool_output_bytes"].Layer != "env:NANITE_HARNESS_TOOL_OUTPUT_BYTES" {
		t.Errorf("both: %v %+v", res.Values.ToolOutputBytes, res.Sources["tool_output_bytes"])
	}
	// Legacy beats a launch override, like any environment layer.
	res = mustResolve(t, r, Inputs{Launch: Layer{Limits: limitsWith(ptr64(1234567))}, Getenv: get(map[string]string{"NANITE_TOOL_TURN_CEILING_BYTES": "50000"})})
	if *res.Values.ToolOutputBytes != 50000 {
		t.Errorf("legacy env should outrank launch: %d", *res.Values.ToolOutputBytes)
	}
	for _, bad := range []string{"lots", "-4", ""} {
		res = mustResolve(t, r, Inputs{Getenv: get(map[string]string{"NANITE_TOOL_TURN_CEILING_BYTES": bad})})
		if res.Values.ToolOutputBytes != nil {
			t.Errorf("legacy %q should be ignored, got %d", bad, *res.Values.ToolOutputBytes)
		}
	}
	res = mustResolve(t, r, Inputs{Getenv: get(map[string]string{"NANITE_TOOL_TURN_CEILING_BYTES": "0"})})
	if res.Values.ToolOutputBytes == nil || *res.Values.ToolOutputBytes != 0 || res.Values.TurnCeiling(200_000, -1) != 0 {
		t.Errorf("legacy 0 must still disable the ceiling: %v", res.Values.ToolOutputBytes)
	}
}

func TestToolOutputKnobsValidated(t *testing.T) {
	r := newReg(t, map[string]string{
		"pct.yaml":   "name: pct\nharness: {tool_output_pct: 3}\n",
		"share.yaml": "name: share\nharness: {tool_output_remaining_share: 0}\n",
		"neg.yaml":   "name: neg\nlimits: {tool_output_bytes: -1}\n",
	})
	for _, n := range []string{"pct", "share", "neg"} {
		if _, err := r.Resolve(Inputs{Profile: n, Getenv: noEnv}); err == nil {
			t.Errorf("%s accepted", n)
		}
	}
}

func limitsWith(v *int64) agentcontracts.Limits { return agentcontracts.Limits{ToolOutputBytes: v} }
