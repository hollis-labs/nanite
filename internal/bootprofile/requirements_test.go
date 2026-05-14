package bootprofile

import (
	"errors"
	"strings"
	"testing"
)

// TestResolveRequirements_NoRequirementsIsNoop pins the happy path:
// fully-resolved specs (only text + static slots) drain without an
// error. This is the "compile produces a clean spec" case the
// runtime hookup wants — the cached BootPrompt is already complete.
func TestResolveRequirements_NoRequirementsIsNoop(t *testing.T) {
	spec := &LaunchSpec{ProfileID: "p", Requirements: nil}
	if err := ResolveRequirements(spec); err != nil {
		t.Fatalf("ResolveRequirements(empty) = %v, want nil", err)
	}
}

// TestResolveRequirements_NilSpecIsNoop covers the defensive nil
// branch so a future caller chaining through a missing spec doesn't
// panic.
func TestResolveRequirements_NilSpecIsNoop(t *testing.T) {
	if err := ResolveRequirements(nil); err != nil {
		t.Fatalf("ResolveRequirements(nil) = %v, want nil", err)
	}
}

// TestResolveRequirements_StubsCMDSource is the load-bearing test
// for the scope decision: CW-20260514-0048 explicitly defers cmd/
// http/role_summary/skill_index resolvers, so any spec that uses
// them surfaces a clean ErrRequirementUnsupported with the slot
// name + source type. This separates "operator authored a slot we
// haven't wired" from "compile produced a broken spec".
func TestResolveRequirements_StubsCMDSource(t *testing.T) {
	spec := &LaunchSpec{
		ProfileID: "p",
		Requirements: []Requirement{
			{Slot: "recap", Type: "cmd", Run: "git log"},
		},
	}
	err := ResolveRequirements(spec)
	if err == nil {
		t.Fatal("ResolveRequirements(cmd slot) = nil, want error")
	}
	if !errors.Is(err, ErrRequirementUnsupported) {
		t.Fatalf("err = %v, want errors.Is ErrRequirementUnsupported", err)
	}
	// The error message must name the slot AND the source type so
	// the operator can find the bad YAML quickly.
	msg := err.Error()
	for _, want := range []string{"p", "recap", "cmd"} {
		if !strings.Contains(msg, want) {
			t.Errorf("err message %q missing %q", msg, want)
		}
	}
}

// TestResolveRequirements_StubsAllDeferredTypes verifies the four
// deferred source types each surface the stub error individually.
// Parameterized so a future ticket can replace a single case with
// a real resolver implementation without breaking the others.
func TestResolveRequirements_StubsAllDeferredTypes(t *testing.T) {
	cases := []struct {
		typ string
	}{
		{"cmd"},
		{"http"},
		{"role_summary"},
		{"skill_index"},
	}
	for _, c := range cases {
		t.Run(c.typ, func(t *testing.T) {
			spec := &LaunchSpec{
				ProfileID:    "p",
				Requirements: []Requirement{{Slot: "s", Type: c.typ}},
			}
			err := ResolveRequirements(spec)
			if !errors.Is(err, ErrRequirementUnsupported) {
				t.Fatalf("type %q err = %v, want ErrRequirementUnsupported", c.typ, err)
			}
		})
	}
}
