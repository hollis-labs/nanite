package store

import "testing"

func TestValidateAgentMultiAgentFields_ActivationModeThreeValueEnum(t *testing.T) {
	valid := []string{"", "singleton", "fresh-per-wake", "concurrent"}
	for _, v := range valid {
		a := &AgentProfile{ActivationMode: v}
		if err := validateAgentMultiAgentFields(a); err != nil {
			t.Errorf("activation_mode %q: unexpected error %v", v, err)
		}
	}
	a := &AgentProfile{ActivationMode: "instance"}
	if err := validateAgentMultiAgentFields(a); err == nil {
		t.Error("activation_mode 'instance': expected rejection, got nil (it was retired by migration 117)")
	}
}

// TestDefaultActivationModeForClass pins the class -> activation_mode
// default mapping documented on DefaultActivationModeForClass: process and
// template both get the non-blocking 'fresh-per-wake' default (matching
// durable_wake.go's pre-migration-116 behavior for process, and
// deliberately extending it to template -- see this task's Work Log and
// the migration's own Up comment for the CW-20260817 template-class latent
// bug this closes); every other class (including unrecognized ones)
// defaults to the blocking 'singleton'.
func TestDefaultActivationModeForClass(t *testing.T) {
	cases := map[string]string{
		"process":  "fresh-per-wake",
		"template": "fresh-per-wake",
		"advisor":  "singleton",
		"harness":  "singleton",
		"":         "singleton",
		"bogus":    "singleton",
	}
	for class, want := range cases {
		if got := DefaultActivationModeForClass(class); got != want {
			t.Errorf("DefaultActivationModeForClass(%q) = %q, want %q", class, got, want)
		}
	}
}
