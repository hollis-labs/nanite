package bootprofile

import (
	"errors"
	"os"
	"path/filepath"
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

// TestResolveRequirements_UnknownTypeStillErrors pins that a
// Requirement carrying a Type with no shared resolver still fails
// fast with ErrRequirementUnsupported. CW-20260515-0026 wired the
// four known deferred kinds (cmd/http/role_summary/skill_index)
// through the shared provider, but the unknown-type guard remains so
// a malformed catalog entry surfaces clearly.
func TestResolveRequirements_UnknownTypeStillErrors(t *testing.T) {
	spec := &LaunchSpec{
		ProfileID: "p",
		Requirements: []Requirement{
			{Slot: "recap", Type: "telepathy"},
		},
	}
	err := ResolveRequirements(spec)
	if err == nil {
		t.Fatal("ResolveRequirements(unknown type) = nil, want error")
	}
	if !errors.Is(err, ErrRequirementUnsupported) {
		t.Fatalf("err = %v, want errors.Is ErrRequirementUnsupported", err)
	}
	msg := err.Error()
	for _, want := range []string{"p", "recap", "telepathy"} {
		if !strings.Contains(msg, want) {
			t.Errorf("err message %q missing %q", msg, want)
		}
	}
}

// TestResolveRequirements_CmdSlotResolves pins that a cmd Requirement
// now resolves through the shared CmdResolver: stdout is folded into
// spec.Slots and the prompt re-rendered. CW-20260515-0026.
func TestResolveRequirements_CmdSlotResolves(t *testing.T) {
	spec := &LaunchSpec{
		ProfileID: "p",
		UILabel:   "P",
		Identity:  Identity{LineageAlias: "p"},
		Slots:     map[string]string{},
		Requirements: []Requirement{
			{Slot: "recap", Type: "cmd", Run: "printf 'hello-recap'"},
		},
	}
	if err := ResolveRequirements(spec); err != nil {
		t.Fatalf("ResolveRequirements(cmd) = %v, want nil", err)
	}
	if len(spec.Requirements) != 0 {
		t.Fatalf("Requirements not drained: %+v", spec.Requirements)
	}
	if got := spec.Slots["recap"]; got != "hello-recap" {
		t.Fatalf("recap slot = %q, want %q", got, "hello-recap")
	}
	if !strings.Contains(spec.BootPrompt, "hello-recap") {
		t.Fatalf("BootPrompt missing resolved cmd output: %q", spec.BootPrompt)
	}
}

// TestResolveRequirements_CmdFailureSurfaces pins that a cmd that
// exits non-zero surfaces as a pointed error naming the slot, rather
// than silently landing an empty section.
func TestResolveRequirements_CmdFailureSurfaces(t *testing.T) {
	spec := &LaunchSpec{
		ProfileID: "p",
		Slots:     map[string]string{},
		Requirements: []Requirement{
			{Slot: "recap", Type: "cmd", Run: "exit 3"},
		},
	}
	err := ResolveRequirements(spec)
	if err == nil {
		t.Fatal("ResolveRequirements(failing cmd) = nil, want error")
	}
	if !strings.Contains(err.Error(), "recap") {
		t.Fatalf("err %q should name the failing slot", err)
	}
}

// TestResolveRequirements_RoleSummaryResolves pins the role_summary
// kind: the role markdown file body is folded into spec.Slots.
func TestResolveRequirements_RoleSummaryResolves(t *testing.T) {
	dir := t.TempDir()
	rolePath := filepath.Join(dir, "worker.md")
	body := "# Backend\n\nYou are a backend engineer.\n"
	if err := os.WriteFile(rolePath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := &LaunchSpec{
		ProfileID: "p",
		UILabel:   "P",
		Identity:  Identity{LineageAlias: "p"},
		Slots:     map[string]string{},
		Requirements: []Requirement{
			{Slot: "agent", Type: "role_summary", Path: rolePath},
		},
	}
	if err := ResolveRequirements(spec); err != nil {
		t.Fatalf("ResolveRequirements(role_summary) = %v, want nil", err)
	}
	if !strings.Contains(spec.Slots["agent"], "backend engineer") {
		t.Fatalf("agent slot missing role body: %q", spec.Slots["agent"])
	}
}

// TestResolveRequirements_SkillIndexResolves pins the skill_index
// kind: discovered skills under a caller-supplied root are rendered
// into the slot. The root is an explicit Requirement.Roots entry.
func TestResolveRequirements_SkillIndexResolves(t *testing.T) {
	dir := t.TempDir()
	skillMD := "---\nname: adr\ndescription: Capture an architectural decision\ntriggers:\n  - /adr\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "adr.md"), []byte(skillMD), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := &LaunchSpec{
		ProfileID: "p",
		UILabel:   "P",
		Identity:  Identity{LineageAlias: "p"},
		Slots:     map[string]string{},
		Requirements: []Requirement{
			{Slot: "skills", Type: "skill_index", Roots: []string{dir}},
		},
	}
	if err := ResolveRequirements(spec); err != nil {
		t.Fatalf("ResolveRequirements(skill_index) = %v, want nil", err)
	}
	if !strings.Contains(spec.Slots["skills"], "/adr") {
		t.Fatalf("skills slot missing discovered skill: %q", spec.Slots["skills"])
	}
}
