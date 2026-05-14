package bootprofile

import (
	"errors"
	"strings"
	"testing"
)

// TestCompileFor_AppliesSessionVars verifies CompileFor produces a
// FRESH spec whose slot bodies have the caller-supplied vars applied.
// Pinning this is the heart of CW-20260514-0048: the cached spec is
// for listing (compiled with empty vars at Reload), the boot-time
// spec is compiled per-session with session-scoped vars.
func TestCompileFor_AppliesSessionVars(t *testing.T) {
	root := t.TempDir()
	writeYAML(t, root, "boot-profiles/p.yaml",
		`id: p
display_name: "P"
launch: l
identity:
  lineage_alias: p
slots:
  agent:
    type: text
    content: "hello {{session_id}}"
`)
	writeYAML(t, root, "launches/l.yaml",
		`id: l
provider: pty-claude
`)
	// The cached spec compile FAILS for empty vars because {{session_id}}
	// is missing — NewRegistry surfaces the partial-load error but still
	// returns a usable registry (compile errors are scoped to the broken
	// profile, others load). The test only cares about the load_for_session
	// path so we deliberately ignore the registry-load partial error.
	reg, _ := NewRegistry(root)
	if reg == nil {
		t.Fatal("NewRegistry returned nil registry on partial-load error")
	}
	if _, ok := reg.Lookup("p"); ok {
		t.Fatalf("expected cache miss for profile p (empty-vars compile should fail on {{session_id}})")
	}

	// CompileFor with the var supplied produces a clean spec.
	spec, err := reg.CompileFor("p", Vars{"session_id": "sess-42"})
	if err != nil {
		t.Fatalf("CompileFor: %v", err)
	}
	if got := spec.Slots["agent"]; got != "hello sess-42" {
		t.Fatalf("CompileFor agent slot = %q, want %q", got, "hello sess-42")
	}
	if !strings.Contains(spec.BootPrompt, "hello sess-42") {
		t.Fatalf("CompileFor BootPrompt missing substituted content: %q", spec.BootPrompt)
	}
}

// TestCompileFor_AcceptsEncodedID confirms CompileFor accepts both
// the bare profile id ("p") AND the encoded provider id form
// ("bootprofile:p"). The chat layer holds the encoded form coming
// off session.Provider; not having to decode-before-call is a small
// ergonomic win that the registry test pins.
func TestCompileFor_AcceptsEncodedID(t *testing.T) {
	root := writeCatalogFixture(t)
	reg, err := NewRegistry(root)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	bare, err := reg.CompileFor("nanite.backend.main", nil)
	if err != nil {
		t.Fatalf("CompileFor(bare): %v", err)
	}
	encoded, err := reg.CompileFor("bootprofile:nanite.backend.main", nil)
	if err != nil {
		t.Fatalf("CompileFor(encoded): %v", err)
	}
	if bare.ProfileID != encoded.ProfileID {
		t.Fatalf("bare vs encoded ProfileID mismatch: %q vs %q", bare.ProfileID, encoded.ProfileID)
	}
}

// TestCompileFor_ReturnsProfileNotFound pins the not-in-catalog
// branch so the chat layer can branch on errors.Is(err, ErrProfileNotFound)
// for a pointed error.
func TestCompileFor_ReturnsProfileNotFound(t *testing.T) {
	root := writeCatalogFixture(t)
	reg, err := NewRegistry(root)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if _, err := reg.CompileFor("does.not.exist", nil); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("CompileFor(missing) err = %v, want ErrProfileNotFound", err)
	}
}

// TestCompileFor_NilReceiver covers the "no catalog configured"
// branch.
func TestCompileFor_NilReceiver(t *testing.T) {
	var reg *Registry
	if _, err := reg.CompileFor("p", nil); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("nil Registry CompileFor err = %v, want ErrProfileNotFound", err)
	}
}

// TestCompileFor_EmptyProfileID rejects the zero string.
func TestCompileFor_EmptyProfileID(t *testing.T) {
	root := writeCatalogFixture(t)
	reg, _ := NewRegistry(root)
	if _, err := reg.CompileFor("", nil); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("CompileFor(\"\") err = %v, want ErrProfileNotFound", err)
	}
}

// TestCompileFor_DoesNotMutateCachedSpec is the load-bearing
// invariant for the dropdown / chat layer split: CompileFor must
// produce a fresh spec, not mutate the cached one.
func TestCompileFor_DoesNotMutateCachedSpec(t *testing.T) {
	root := writeCatalogFixture(t)
	reg, _ := NewRegistry(root)
	cached, ok := reg.Lookup("nanite.backend.main")
	if !ok {
		t.Fatal("expected cached spec")
	}
	cachedSlot := cached.Slots["agent"]

	// Mutate the *returned* spec to confirm it's a separate value.
	spec, err := reg.CompileFor("nanite.backend.main", Vars{"unused": "X"})
	if err != nil {
		t.Fatalf("CompileFor: %v", err)
	}
	spec.Slots["agent"] = "mutated"

	again, _ := reg.Lookup("nanite.backend.main")
	if again.Slots["agent"] != cachedSlot {
		t.Fatalf("cached spec leaked mutation: agent slot now %q", again.Slots["agent"])
	}
}
