package bootprofile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEncodeDecodeRoundTrip pins the chosen ID format
// (`bootprofile:<profile_id>`) so a future ticket cannot quietly
// reshape the encoding without taking the dropdown / runtime hookup
// callers with it. ProfileIDs are domain-controlled (we pick them),
// so we don't bother with URL-escaping — but the test still covers
// a couple of realistic shapes (dotted ids, single-token ids).
func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []struct {
		name      string
		profileID string
		want      string
	}{
		{"dotted", "nanite.backend.main", "bootprofile:nanite.backend.main"},
		{"hyphen", "nanite-frontend", "bootprofile:nanite-frontend"},
		{"single", "backend", "bootprofile:backend"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := EncodeProviderID(c.profileID)
			if got != c.want {
				t.Fatalf("EncodeProviderID(%q) = %q, want %q", c.profileID, got, c.want)
			}
			back, ok := DecodeProviderID(got)
			if !ok {
				t.Fatalf("DecodeProviderID(%q) ok=false", got)
			}
			if back != c.profileID {
				t.Fatalf("DecodeProviderID(%q) = %q, want %q", got, back, c.profileID)
			}
			if !IsProviderID(got) {
				t.Fatalf("IsProviderID(%q) = false", got)
			}
		})
	}
}

func TestEncodeProviderID_EmptyReturnsEmpty(t *testing.T) {
	if got := EncodeProviderID(""); got != "" {
		t.Fatalf("EncodeProviderID(\"\") = %q, want empty", got)
	}
}

func TestDecodeProviderID_RejectsNonNamespaced(t *testing.T) {
	cases := []string{
		"",
		"pty-claude",
		"anthropic",
		"bootprofile:",          // empty suffix
		"BOOTPROFILE:something", // case-sensitive prefix on purpose
		"sub-claude",
	}
	for _, c := range cases {
		if _, ok := DecodeProviderID(c); ok {
			t.Errorf("DecodeProviderID(%q) ok=true, want false", c)
		}
		if IsProviderID(c) {
			t.Errorf("IsProviderID(%q) = true, want false", c)
		}
	}
}

func TestRegistry_NilSafe(t *testing.T) {
	var r *Registry
	if got := r.List(); got != nil {
		t.Fatalf("nil List = %v, want nil", got)
	}
	if _, ok := r.Lookup("anything"); ok {
		t.Fatalf("nil Lookup ok=true")
	}
	if !r.IsEmpty() {
		t.Fatalf("nil IsEmpty = false")
	}
	if err := r.Reload(); err != nil {
		t.Fatalf("nil Reload returned err: %v", err)
	}
	if got := r.CatalogPath(); got != "" {
		t.Fatalf("nil CatalogPath = %q", got)
	}
}

func TestNewRegistry_EmptyPathIsEmpty(t *testing.T) {
	r, err := NewRegistry("")
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if !r.IsEmpty() {
		t.Fatalf("expected empty registry for empty path, got %d specs", len(r.List()))
	}
}

func TestNewRegistry_MissingPathIsEmpty(t *testing.T) {
	r, err := NewRegistry(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if !r.IsEmpty() {
		t.Fatalf("expected empty registry for missing path, got %d specs", len(r.List()))
	}
}

func TestNewRegistry_LoadsProfiles(t *testing.T) {
	root := writeCatalogFixture(t)
	r, err := NewRegistry(root)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	specs := r.List()
	if len(specs) != 2 {
		t.Fatalf("expected 2 specs, got %d", len(specs))
	}
	// List is sorted by ProfileID — depend on it for the dropdown
	// stability rationale.
	if specs[0].ProfileID != "nanite.backend.main" || specs[1].ProfileID != "nanite.frontend.main" {
		t.Fatalf("specs not in sorted order: %v", []string{specs[0].ProfileID, specs[1].ProfileID})
	}

	// Lookup by encoded id should work.
	encoded := EncodeProviderID("nanite.backend.main")
	got, ok := r.Lookup(encoded)
	if !ok {
		t.Fatalf("Lookup(%q) ok=false", encoded)
	}
	if got.ProviderAlias != "pty-claude" {
		t.Fatalf("Lookup.ProviderAlias = %q, want pty-claude", got.ProviderAlias)
	}

	// Lookup by bare profile id should also work — tests in 0048
	// land that path when the runtime already has a ProfileID.
	gotBare, ok := r.Lookup("nanite.backend.main")
	if !ok || gotBare != got {
		t.Fatalf("Lookup(bare) returned different result")
	}
	if _, ok := r.Lookup("does-not-exist"); ok {
		t.Fatalf("Lookup(missing) ok=true")
	}
	if _, ok := r.Lookup(""); ok {
		t.Fatalf("Lookup(empty) ok=true")
	}
}

func TestRegistry_Reload_PicksUpNewProfile(t *testing.T) {
	root := writeCatalogFixture(t)
	r, err := NewRegistry(root)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if got := len(r.List()); got != 2 {
		t.Fatalf("initial: expected 2 specs, got %d", got)
	}

	// Add a third profile + a paired launch and reload.
	writeYAML(t, root, "boot-profiles/nanite.reviewer.main.yaml",
		`id: nanite.reviewer.main
display_name: "Nanite — Reviewer"
launch: nanite-codex
identity:
  lineage_alias: nanite.reviewer.main
  role: reviewer
slots:
  agent:
    type: text
    content: "you review"
`)
	writeYAML(t, root, "launches/nanite-codex.yaml",
		`id: nanite-codex
provider: pty-codex
workdir: /tmp/nanite
ui_label: "Nanite (Codex PTY)"
`)

	if err := r.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := len(r.List()); got != 3 {
		t.Fatalf("after Reload: expected 3 specs, got %d", got)
	}
	if _, ok := r.Lookup("nanite.reviewer.main"); !ok {
		t.Fatalf("Reload did not pick up the new profile")
	}
}

func TestRegistry_Reload_DropsRemovedProfile(t *testing.T) {
	root := writeCatalogFixture(t)
	r, err := NewRegistry(root)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if _, ok := r.Lookup("nanite.frontend.main"); !ok {
		t.Fatalf("precondition: frontend not loaded")
	}
	if err := os.Remove(filepath.Join(root, "boot-profiles", "nanite.frontend.main.yaml")); err != nil {
		t.Fatalf("remove fixture: %v", err)
	}
	if err := r.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if _, ok := r.Lookup("nanite.frontend.main"); ok {
		t.Fatalf("Reload kept the removed profile")
	}
}

// TestRegistry_Reload_TolerantOfPerProfileCompileError documents the
// "show the profiles that work even when one is misconfigured"
// contract from registry.go. We construct a catalog with one good
// profile and one profile that names an undefined launch — the
// launch reference must fail compile, but the good profile must
// still land in the cache and the returned error must mention the
// bad id so the operator knows what to fix.
//
// Validation note: bootprofile.LoadCatalog will FAIL on a missing
// launch reference at compile time (CompileFromCatalog returns
// ErrLaunchNotFound). To exercise the per-profile compile failure
// without aborting LoadCatalog, we use a profile that compiles fine
// on its own — no launch — so it should land in the cache; this
// test mainly proves the surface stays useful when partial data is
// present.
func TestRegistry_LoadsLaunchlessProfile(t *testing.T) {
	root := t.TempDir()
	writeYAML(t, root, "boot-profiles/launchless.yaml",
		`id: launchless
display_name: "Launchless"
identity:
  lineage_alias: launchless
  role: backend
slots:
  agent:
    type: text
    content: "no launch"
`)
	r, err := NewRegistry(root)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	spec, ok := r.Lookup("launchless")
	if !ok {
		t.Fatalf("launchless profile not loaded")
	}
	if spec.LaunchID != "" {
		t.Fatalf("launchless.LaunchID = %q, want empty", spec.LaunchID)
	}
	if spec.Provider != "" {
		t.Fatalf("launchless.Provider = %q, want empty", spec.Provider)
	}
	if spec.UILabel != "Launchless" {
		t.Fatalf("launchless.UILabel = %q", spec.UILabel)
	}
}

func TestRegistry_Reload_SurfacesCompileErrors(t *testing.T) {
	root := t.TempDir()
	// Good profile (no launch, compiles cleanly).
	writeYAML(t, root, "boot-profiles/good.yaml",
		`id: good
identity:
  lineage_alias: good
slots:
  agent:
    type: text
    content: "ok"
`)
	// Bad profile: declares a launch that doesn't exist. Compile
	// returns ErrLaunchNotFound. Registry should still keep the good
	// one and report the bad id in the returned error.
	writeYAML(t, root, "boot-profiles/bad.yaml",
		`id: bad
identity:
  lineage_alias: bad
launch: nonexistent-launch
slots:
  agent:
    type: text
    content: "bad"
`)
	r, err := NewRegistry(root)
	if err == nil {
		t.Fatalf("expected compile-error report, got nil")
	}
	if !strings.Contains(err.Error(), "bad:") {
		t.Fatalf("error %q should mention the bad profile id", err)
	}
	if _, ok := r.Lookup("good"); !ok {
		t.Fatalf("good profile dropped due to bad neighbor")
	}
	if _, ok := r.Lookup("bad"); ok {
		t.Fatalf("bad profile present in cache despite compile failure")
	}
}

// writeCatalogFixture stamps a two-profile catalog on disk and
// returns the root. Used as a baseline for the registry tests above.
func writeCatalogFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeYAML(t, root, "boot-profiles/nanite.backend.main.yaml",
		`id: nanite.backend.main
display_name: "Nanite — Backend"
launch: nanite-claude
identity:
  lineage_alias: nanite.backend.main
  role: backend
slots:
  agent:
    type: text
    content: "you build"
`)
	writeYAML(t, root, "boot-profiles/nanite.frontend.main.yaml",
		`id: nanite.frontend.main
display_name: "Nanite — Frontend"
launch: nanite-claude
identity:
  lineage_alias: nanite.frontend.main
  role: frontend
slots:
  agent:
    type: text
    content: "you paint"
`)
	writeYAML(t, root, "launches/nanite-claude.yaml",
		`id: nanite-claude
provider: pty-claude
workdir: /tmp/nanite
ui_label: "Nanite (Claude PTY)"
`)
	return root
}
