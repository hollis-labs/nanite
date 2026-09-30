package harnessprofile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func i(v int) *int { return &v }

func newReg(t *testing.T, files map[string]string) *Registry {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r, err := NewRegistry(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func noEnv(string) (string, bool) { return "", false }

func mustResolve(t *testing.T, r *Registry, in Inputs) *Resolved {
	t.Helper()
	if in.Getenv == nil {
		in.Getenv = noEnv
	}
	res, err := r.Resolve(in)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// The default profile with no other layer is exactly the compiled-in behavior.
func TestDefaultProfileIsComputedDefaults(t *testing.T) {
	res := mustResolve(t, newReg(t, nil), Inputs{})
	want := Values{
		IdleTimeout: 900 * time.Second, SubagentIdleTimeout: 300 * time.Second,
		HardCeiling: 200, ConsecutiveFailCap: 3, RunawayFailCap: 10, PerToolCap: 0, MaxConcurrentTools: 8,
		CompactPreviewBytes: 512, PreviewPct: 0.004, PreviewMinBytes: 4000, PreviewMaxBytes: 32000,
		ToolOutputPct: 0.03, ToolOutputMinBytes: 24576, ToolOutputMaxBytes: 524288,
		ToolOutputRemainingShare: 0.25, ToolOutputRemainingFloor: 4096,
	}
	if res.Values != want {
		t.Errorf("values = %+v\nwant     %+v", res.Values, want)
	}
	if res.Profile != "default" {
		t.Errorf("profile = %q", res.Profile)
	}
	for key, s := range res.Sources {
		if s.Layer != "computed" || s.Clamped {
			t.Errorf("source[%s] = %+v, want computed", key, s)
		}
	}
	if len(res.Unenforced) != 0 {
		t.Errorf("unenforced = %v", res.Unenforced)
	}
}

// Each layer overrides the one below it, and the recorded source names the
// layer that actually supplied the final value.
func TestPrecedenceAndSources(t *testing.T) {
	r := newReg(t, map[string]string{
		"exp.yaml": `
name: exp
extends: conservative
harness: {hard_ceiling: 150}
models:
  "claude-*": {harness: {hard_ceiling: 120}}
  "claude-opus-*": {harness: {hard_ceiling: 110}}
`,
	})
	env := func(k string) (string, bool) {
		if k == "NANITE_HARNESS_HARD_CEILING" {
			return "60", true
		}
		return "", false
	}
	base := Inputs{Profile: "exp", Model: "claude-opus-5", Getenv: noEnv}
	// Trace hard_ceiling upward layer by layer.
	steps := []struct {
		name   string
		in     func(Inputs) Inputs
		want   int
		source string
	}{
		{"model block (most specific pattern)", func(x Inputs) Inputs { return x }, 110, "profile:exp/model:claude-opus-*"},
		{"agent beats profile and model", func(x Inputs) Inputs {
			x.Agent = Layer{Harness: Knobs{HardCeiling: i(90)}}
			return x
		}, 90, "agent"},
		{"launch beats agent", func(x Inputs) Inputs {
			x.Launch = Layer{Harness: Knobs{HardCeiling: i(80)}}
			return x
		}, 80, "launch"},
		{"env beats launch", func(x Inputs) Inputs { x.Getenv = env; return x }, 60, "env:NANITE_HARNESS_HARD_CEILING"},
	}
	in := base
	for _, st := range steps {
		in = st.in(in)
		res := mustResolve(t, r, in)
		if res.Values.HardCeiling != st.want || res.Sources["hard_ceiling"].Layer != st.source {
			t.Errorf("%s: got %d from %q, want %d from %q", st.name, res.Values.HardCeiling, res.Sources["hard_ceiling"].Layer, st.want, st.source)
		}
	}
	// A different model does not see the model blocks.
	other := mustResolve(t, r, Inputs{Profile: "exp", Model: "gpt-5", Getenv: noEnv})
	if other.Values.HardCeiling != 150 || other.Sources["hard_ceiling"].Layer != "profile:exp" {
		t.Errorf("non-matching model: %d from %q", other.Values.HardCeiling, other.Sources["hard_ceiling"].Layer)
	}
	// A value the child does not state is inherited and attributed to the
	// profile that stated it.
	if other.Values.RunawayFailCap != 5 || other.Sources["runaway_fail_cap"].Layer != "profile:conservative" {
		t.Errorf("inherited: %d from %q", other.Values.RunawayFailCap, other.Sources["runaway_fail_cap"].Layer)
	}
	if other.Sources["compact_preview_bytes"].Layer != "computed" {
		t.Errorf("untouched knob source = %q", other.Sources["compact_preview_bytes"].Layer)
	}
}

func TestAppSettingsSitBelowTheProfile(t *testing.T) {
	r := newReg(t, nil)
	app := Layer{Harness: Knobs{PerToolCap: i(150)}}
	res := mustResolve(t, r, Inputs{AppSettings: app})
	if res.Values.PerToolCap != 150 || res.Sources["per_tool_cap"].Layer != "app-settings" {
		t.Errorf("default profile leaves app settings in force: %d from %q", res.Values.PerToolCap, res.Sources["per_tool_cap"].Layer)
	}
	res = mustResolve(t, r, Inputs{Profile: "conservative", AppSettings: app})
	if res.Values.PerToolCap != 50 || res.Sources["per_tool_cap"].Layer != "profile:conservative" {
		t.Errorf("a profile that states the knob wins over app settings: %d from %q", res.Values.PerToolCap, res.Sources["per_tool_cap"].Layer)
	}
}

// Host maximums are applied last to every layer, including env, and the source
// keeps the layer that asked while flagging the clamp.
func TestClampsApplyLastAndAreRecorded(t *testing.T) {
	r := newReg(t, nil)
	env := func(k string) (string, bool) {
		switch k {
		case "NANITE_HARNESS_HARD_CEILING":
			return "99999999", true
		case "NANITE_HARNESS_IDLE_TIMEOUT_MS":
			return "10", true
		}
		return "", false
	}
	res := mustResolve(t, r, Inputs{Getenv: env, Launch: Layer{Harness: Knobs{CompactPreviewBytes: i(1)}}})
	if res.Values.HardCeiling != 10000 {
		t.Errorf("hard ceiling = %d, want clamped to 10000", res.Values.HardCeiling)
	}
	if s := res.Sources["hard_ceiling"]; s.Layer != "env:NANITE_HARNESS_HARD_CEILING" || !s.Clamped || s.Requested != "99999999" {
		t.Errorf("hard_ceiling source = %+v", s)
	}
	if res.Values.IdleTimeout != time.Second {
		t.Errorf("idle = %v, want clamped up to 1s", res.Values.IdleTimeout)
	}
	if s := res.Sources["idle_timeout_ms"]; !s.Clamped || s.Layer != "env:NANITE_HARNESS_IDLE_TIMEOUT_MS" {
		t.Errorf("idle source = %+v", s)
	}
	if res.Values.CompactPreviewBytes != 64 || !res.Sources["compact_preview_bytes"].Clamped || res.Sources["compact_preview_bytes"].Layer != "launch" {
		t.Errorf("compact = %d %+v", res.Values.CompactPreviewBytes, res.Sources["compact_preview_bytes"])
	}
	if s := res.Sources["consecutive_fail_cap"]; s.Clamped {
		t.Errorf("an in-range value was marked clamped: %+v", s)
	}
}

func TestCouplingClamps(t *testing.T) {
	r := newReg(t, nil)
	res := mustResolve(t, r, Inputs{Launch: Layer{Harness: Knobs{ConsecutiveFailCap: i(8), RunawayFailCap: i(4), PreviewMinBytes: i(9000), PreviewMaxBytes: i(5000)}}})
	if res.Values.RunawayFailCap != 8 || !res.Sources["runaway_fail_cap"].Clamped {
		t.Errorf("runaway = %d %+v: the terminal cap must not sit below the warning cap", res.Values.RunawayFailCap, res.Sources["runaway_fail_cap"])
	}
	if res.Values.PreviewMaxBytes != 9000 || !res.Sources["preview_max_bytes"].Clamped {
		t.Errorf("preview max = %d %+v", res.Values.PreviewMaxBytes, res.Sources["preview_max_bytes"])
	}
}

func TestUnenforcedLimitsAreCarriedAndListed(t *testing.T) {
	r := newReg(t, map[string]string{"budgeted.yaml": "name: budgeted\nlimits: {max_duration_ms: 60000, token_budget: 5000, tool_output_bytes: 65536}\n"})
	res := mustResolve(t, r, Inputs{Profile: "budgeted"})
	if res.Limits.MaxDurationMs == nil || *res.Limits.MaxDurationMs != 60000 || res.Limits.TokenBudget == nil {
		t.Fatalf("limits = %+v", res.Limits)
	}
	if strings.Join(res.Unenforced, ",") != "max_duration_ms,token_budget" {
		t.Errorf("unenforced = %v", res.Unenforced)
	}
	// tool_output_bytes is enforced (the tool-output ceiling), so it is not
	// listed as unenforced, and it reaches the resolved values.
	if res.Limits.ToolOutputBytes == nil || *res.Limits.ToolOutputBytes != 65536 || res.Values.ToolOutputBytes == nil || *res.Values.ToolOutputBytes != 65536 || res.Sources["tool_output_bytes"].Layer != "profile:budgeted" {
		t.Errorf("tool_output_bytes = %+v %+v", res.Limits.ToolOutputBytes, res.Sources["tool_output_bytes"])
	}
	if res.Sources["max_duration_ms"].Layer != "profile:budgeted" {
		t.Errorf("source = %+v", res.Sources["max_duration_ms"])
	}
}

func TestBuiltinProfiles(t *testing.T) {
	r := newReg(t, nil)
	c := mustResolve(t, r, Inputs{Profile: "conservative"})
	if c.Values.HardCeiling != 100 || c.Values.RunawayFailCap != 5 || c.Values.IdleTimeout != 300*time.Second {
		t.Errorf("conservative = %+v", c.Values)
	}
	d := mustResolve(t, r, Inputs{Profile: "dev"})
	if d.Values.IdleTimeout != time.Hour || d.Values.HardCeiling != 400 || d.Values.RunawayFailCap != 10 {
		t.Errorf("dev = %+v", d.Values)
	}
	if got := strings.Join(r.Names(), ","); got != "conservative,default,dev" {
		t.Errorf("names = %s", got)
	}
}

func TestUnknownAndInvalidProfilesFailLoudly(t *testing.T) {
	r := newReg(t, map[string]string{
		"typo.yaml":   "name: typo\nharness: {hard_celing: 5}\n",
		"neg.yaml":    "name: neg\nharness: {hard_ceiling: -1}\n",
		"pct.yaml":    "name: pct\nharness: {preview_pct: 2}\n",
		"a.yaml":      "name: a\nextends: b\n",
		"b.yaml":      "name: b\nextends: a\n",
		"self.yaml":   "name: self\nextends: self\n",
		"wrong.yaml":  "name: other\n",
		"pat.yaml":    "name: pat\nmodels: {\"[\": {}}\n",
		"orphan.yaml": "name: orphan\nextends: nothere\n",
	})
	for name, want := range map[string]string{
		"nope":   `unknown harness profile "nope" (available:`,
		"typo":   "hard_celing",
		"neg":    "must not be negative",
		"pct":    "preview_pct",
		"a":      "extends cycle",
		"self":   "extends itself",
		"wrong":  "does not match the file name",
		"pat":    "model pattern",
		"orphan": "unknown harness profile",
	} {
		_, err := r.Resolve(Inputs{Profile: name, Getenv: noEnv})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("profile %q: err = %v, want containing %q", name, err, want)
		}
	}
}

func TestEnvErrorsAreNotSilent(t *testing.T) {
	r := newReg(t, nil)
	for _, tc := range []struct{ k, v string }{
		{"NANITE_HARNESS_HARD_CEILING", "lots"},
		{"NANITE_HARNESS_HARD_CEILING", "-5"},
		{"NANITE_HARNESS_PREVIEW_PCT", "0"},
	} {
		_, err := r.Resolve(Inputs{Getenv: func(k string) (string, bool) {
			if k == tc.k {
				return tc.v, true
			}
			return "", false
		}})
		if err == nil || !strings.Contains(err.Error(), tc.k) {
			t.Errorf("%s=%s: err = %v", tc.k, tc.v, err)
		}
	}
}

func TestUserFileCannotShadowBuiltin(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "default.yaml"), []byte("name: default\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRegistry(dir, ""); err == nil || !strings.Contains(err.Error(), "built-in") {
		t.Errorf("err = %v", err)
	}
}

func TestBadDefaultNameFailsAtStartup(t *testing.T) {
	if _, err := NewRegistry(t.TempDir(), "missing"); err == nil || !strings.Contains(err.Error(), "default profile") {
		t.Errorf("err = %v", err)
	}
	r, err := NewRegistry("", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if got := mustResolve(t, r, Inputs{}).Profile; got != "dev" {
		t.Errorf("configured default = %q", got)
	}
}

// Editing a profile file takes effect on the next resolve, and the digest moves
// with the content so a recorded run can tell the two apart.
func TestFileEditIsPickedUpAndDigestChanges(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "tune.yaml")
	write := func(body string, mod time.Time) {
		t.Helper()
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(file, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	write("name: tune\nharness: {hard_ceiling: 100}\n", now)
	r, err := NewRegistry(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	a := mustResolve(t, r, Inputs{Profile: "tune"})
	write("name: tune\nharness: {hard_ceiling: 300}\n", now.Add(time.Minute))
	b := mustResolve(t, r, Inputs{Profile: "tune"})
	if a.Values.HardCeiling != 100 || b.Values.HardCeiling != 300 {
		t.Errorf("ceilings = %d then %d", a.Values.HardCeiling, b.Values.HardCeiling)
	}
	if a.Digest == b.Digest || !strings.HasPrefix(a.Digest, "sha256:") {
		t.Errorf("digests %q %q", a.Digest, b.Digest)
	}
	if again := mustResolve(t, r, Inputs{Profile: "tune"}); again.Digest != b.Digest {
		t.Error("digest is not stable for unchanged content")
	}
	// The digest covers the extends chain, not just the leaf.
	if c := mustResolve(t, r, Inputs{Profile: "conservative"}); c.Digest == mustResolve(t, r, Inputs{Profile: "default"}).Digest {
		t.Error("digest ignores the chain")
	}
}

func TestEffectiveIdleTimeoutByCaller(t *testing.T) {
	v := mustResolve(t, newReg(t, nil), Inputs{}).Values
	if v.EffectiveIdleTimeout(false) != 900*time.Second || v.EffectiveIdleTimeout(true) != 300*time.Second {
		t.Errorf("%v %v", v.EffectiveIdleTimeout(false), v.EffectiveIdleTimeout(true))
	}
}

func TestNonProfileLayersAreValidated(t *testing.T) {
	r := newReg(t, nil)
	neg := -1
	for name, in := range map[string]Inputs{
		"launch": {Launch: Layer{Harness: Knobs{HardCeiling: &neg}}},
		"agent":  {Agent: Layer{Harness: Knobs{RunawayFailCap: &neg}}},
		"app":    {AppSettings: Layer{Harness: Knobs{PerToolCap: &neg}}},
	} {
		in.Getenv = noEnv
		if _, err := r.Resolve(in); err == nil {
			t.Errorf("%s layer: negative value accepted", name)
		}
	}
}
