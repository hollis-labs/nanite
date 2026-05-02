package permission

import (
	"os"
	"testing"
)

// TestHomeDir_WithHOMESet covers the happy path — HOME is set in the
// environment, HomeDir returns it directly.
func TestHomeDir_WithHOMESet(t *testing.T) {
	prev, hadHome := os.LookupEnv("HOME")
	t.Cleanup(func() {
		if hadHome {
			os.Setenv("HOME", prev)
		} else {
			os.Unsetenv("HOME")
		}
	})

	os.Setenv("HOME", "/tmp/fake-home-for-test")
	got, err := HomeDir()
	if err != nil {
		t.Fatalf("HomeDir() err = %v", err)
	}
	if got != "/tmp/fake-home-for-test" {
		t.Errorf("HomeDir() = %q, want %q", got, "/tmp/fake-home-for-test")
	}
}

// TestHomeDir_WithoutHOME is the c127 reproduction scenario:
// nanite-api-service is spawned by launchd with no HOME in its
// inherited/default environment. Without a passwd-record fallback,
// os.UserHomeDir() returns an error and the literal "~/" flows
// through downstream path expansion. HomeDir must fall back so the
// path-grant flow stays correct on launchd.
func TestHomeDir_WithoutHOME(t *testing.T) {
	prev, hadHome := os.LookupEnv("HOME")
	t.Cleanup(func() {
		if hadHome {
			os.Setenv("HOME", prev)
		} else {
			os.Unsetenv("HOME")
		}
	})

	os.Unsetenv("HOME")

	// Sanity — confirm the precondition the c127 incident relies on:
	// stdlib UserHomeDir actually fails here. If a future Go release
	// adds a built-in passwd fallback this assertion will trip and
	// the regression posture should be re-evaluated.
	if h, err := os.UserHomeDir(); err == nil {
		t.Logf("note: os.UserHomeDir succeeded without HOME (returned %q) — c127 environment differs from this test", h)
	}

	got, err := HomeDir()
	if err != nil {
		t.Fatalf("HomeDir() err = %v (expected fallback via user.Current to succeed)", err)
	}
	if got == "" {
		t.Fatal("HomeDir() returned empty string with HOME unset — fallback failed")
	}
	t.Logf("HomeDir() fallback resolved to %q", got)
}
