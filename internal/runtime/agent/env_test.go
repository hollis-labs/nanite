package agent

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// Test_composeEnv_inherits_minimal_host_env verifies the base host env
// keys are inherited (HOME, PATH, etc.) without leaking the rest of the
// shell environment.
func Test_composeEnv_inherits_minimal_host_env(t *testing.T) {
	t.Setenv("HOME", "/Users/test")
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("LANG", "en_US.UTF-8")
	// Verify a NON-inherited var is not propagated.
	t.Setenv("CUSTOM_VAR_NOT_INHERITED", "should-not-leak")

	got := composeEnv(&store.AgentProfile{}, Options{})

	if got["HOME"] != "/Users/test" {
		t.Errorf("HOME = %q, want /Users/test", got["HOME"])
	}
	if got["PATH"] != "/usr/bin" {
		t.Errorf("PATH = %q, want /usr/bin", got["PATH"])
	}
	if got["LANG"] != "en_US.UTF-8" {
		t.Errorf("LANG = %q, want en_US.UTF-8", got["LANG"])
	}
	if _, leaked := got["CUSTOM_VAR_NOT_INHERITED"]; leaked {
		t.Error("non-inherited var leaked into composed env")
	}
}

// Test_composeEnv_opts_override_inherited verifies caller-supplied env
// (Options.Env) takes precedence over inherited host env.
func Test_composeEnv_opts_override_inherited(t *testing.T) {
	t.Setenv("HOME", "/Users/host")
	got := composeEnv(&store.AgentProfile{}, Options{
		Env: map[string]string{"HOME": "/Users/agent", "EXTRA_KEY": "extra"},
	})
	if got["HOME"] != "/Users/agent" {
		t.Errorf("HOME = %q, want override /Users/agent", got["HOME"])
	}
	if got["EXTRA_KEY"] != "extra" {
		t.Errorf("EXTRA_KEY = %q, want extra", got["EXTRA_KEY"])
	}
}

// Test_composeEnv_nil_profile is defensive — Boot may receive a zero-value
// profile from tests / synthetic spawns.
func Test_composeEnv_nil_profile(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	got := composeEnv(nil, Options{Env: map[string]string{"K": "V"}})
	if got["PATH"] != "/usr/bin" {
		t.Errorf("PATH inheritance broken: %q", got["PATH"])
	}
	if got["K"] != "V" {
		t.Errorf("Options.Env not propagated with nil profile: %v", got)
	}
}
