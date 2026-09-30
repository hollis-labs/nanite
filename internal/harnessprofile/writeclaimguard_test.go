package harnessprofile

import "testing"

func guard(m GuardMode) *GuardMode { return &m }

// deny by default; the dev profile warns; both overridable in every layer, with
// the source recorded, and env winning.
func TestWriteClaimGuardKnob(t *testing.T) {
	r := newReg(t, nil)
	def := mustResolve(t, r, Inputs{})
	if def.Values.WriteClaimGuard != GuardDeny || def.Sources["write_claim_guard"].Layer != "computed" {
		t.Errorf("default: %q from %q", def.Values.WriteClaimGuard, def.Sources["write_claim_guard"].Layer)
	}
	dev := mustResolve(t, r, Inputs{Profile: "dev"})
	if dev.Values.WriteClaimGuard != GuardWarn || dev.Sources["write_claim_guard"].Layer != "profile:dev" {
		t.Errorf("dev: %q from %q", dev.Values.WriteClaimGuard, dev.Sources["write_claim_guard"].Layer)
	}
	// Overridable either way, most specific layer wins.
	up := mustResolve(t, r, Inputs{Profile: "dev", Launch: Layer{Hooks: Hooks{WriteClaimGuard: guard(GuardDeny)}}})
	if up.Values.WriteClaimGuard != GuardDeny || up.Sources["write_claim_guard"].Layer != "launch" {
		t.Errorf("dev tightened by launch: %q from %q", up.Values.WriteClaimGuard, up.Sources["write_claim_guard"].Layer)
	}
	off := mustResolve(t, r, Inputs{Agent: Layer{Hooks: Hooks{WriteClaimGuard: guard(GuardOff)}}})
	if off.Values.WriteClaimGuard != GuardOff || off.Sources["write_claim_guard"].Layer != "agent" {
		t.Errorf("agent off: %q from %q", off.Values.WriteClaimGuard, off.Sources["write_claim_guard"].Layer)
	}
	env := func(k string) (string, bool) {
		if k == "NANITE_HARNESS_WRITE_CLAIM_GUARD" {
			return "ASK", true
		}
		return "", false
	}
	e := mustResolve(t, r, Inputs{Profile: "dev", Launch: Layer{Hooks: Hooks{WriteClaimGuard: guard(GuardDeny)}}, Getenv: env})
	if e.Values.WriteClaimGuard != GuardAsk || e.Sources["write_claim_guard"].Layer != "env:NANITE_HARNESS_WRITE_CLAIM_GUARD" {
		t.Errorf("env: %q from %q", e.Values.WriteClaimGuard, e.Sources["write_claim_guard"].Layer)
	}
	if got := e.Effective().Values["write_claim_guard"]; got != "ask" {
		t.Errorf("recorded value = %v", got)
	}
}

func TestWriteClaimGuardKnobRejectsBadValues(t *testing.T) {
	r := newReg(t, map[string]string{"bad.yaml": "name: bad\nhooks: {write_claim_guard: block}\n"})
	if _, err := r.Resolve(Inputs{Profile: "bad", Getenv: noEnv}); err == nil {
		t.Error("a profile with an unknown mode was accepted")
	}
	bad := GuardMode("block")
	if _, err := r.Resolve(Inputs{Launch: Layer{Hooks: Hooks{WriteClaimGuard: &bad}}, Getenv: noEnv}); err == nil {
		t.Error("an unknown launch override was accepted")
	}
	if _, err := r.Resolve(Inputs{Getenv: func(k string) (string, bool) { return "block", k == "NANITE_HARNESS_WRITE_CLAIM_GUARD" }}); err == nil {
		t.Error("an unknown env value was accepted")
	}
}
