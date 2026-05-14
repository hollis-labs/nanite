package service

// CW-20260514-0048 regression coverage for the chat-resolve layer
// boot-profile decode + compile + stash pipeline.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/bootprofile"
	"github.com/hollis-labs/nanite/internal/store"
)

// writeCatalogForResolveTest is a local helper that mirrors the
// bootprofile package's testdata so we can build a Registry the
// chat-service tests can call CompileFor against. Kept in the
// service package to avoid exporting bootprofile test fixtures.
func writeCatalogForResolveTest(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite := func(rel, body string) {
		t.Helper()
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite("boot-profiles/test.profile.yaml",
		`id: test.profile
display_name: "Test"
launch: test-launch
identity:
  lineage_alias: test
  role: backend
slots:
  agent:
    type: text
    content: "agent for {{session_id}}"
`)
	mustWrite("launches/test-launch.yaml",
		`id: test-launch
provider: pty-claude
workdir: /tmp/test-spec-workdir
ui_label: "Test (Claude PTY)"
env:
  TEST_KEY: spec_value
args:
  - --add-dir
  - /tmp/test-spec-workdir
`)
	mustWrite("boot-profiles/deferred.yaml",
		`id: deferred
display_name: "Deferred"
launch: test-launch
identity:
  lineage_alias: deferred
slots:
  recap:
    type: cmd
    run: "git log -1"
`)
	return root
}

// TestResolveBootProfile_PassthroughNonBootprofile pins the negative
// case: when the providerName isn't a bootprofile:* id, the resolver
// must return it verbatim with a nil spec so the chat layer can
// chain through the legacy path unchanged.
func TestResolveBootProfile_PassthroughNonBootprofile(t *testing.T) {
	s := &chatServiceImpl{}
	cases := []string{"", "anthropic", "pty-claude", "pty", "openai", "sub-claude"}
	for _, name := range cases {
		got, spec, err := s.resolveBootProfile("sess", name, nil, nil)
		if err != nil {
			t.Errorf("resolveBootProfile(%q) err = %v, want nil", name, err)
		}
		if got != name {
			t.Errorf("resolveBootProfile(%q) provider = %q, want passthrough", name, got)
		}
		if spec != nil {
			t.Errorf("resolveBootProfile(%q) spec = %+v, want nil", name, spec)
		}
	}
}

// TestResolveBootProfile_RegistryUnwired surfaces a pointed error
// when a "bootprofile:" id arrives but the registry isn't configured.
// Without this branch the classifier would emit the generic
// "Provider not available" footer which buries the real cause.
func TestResolveBootProfile_RegistryUnwired(t *testing.T) {
	s := &chatServiceImpl{} // bootProfiles == nil
	got, spec, err := s.resolveBootProfile("sess", "bootprofile:test.profile", nil, nil)
	if err == nil {
		t.Fatal("expected error when bootProfiles is nil")
	}
	if !strings.Contains(err.Error(), "bootprofile:test.profile") {
		t.Errorf("err missing provider name: %v", err)
	}
	if got != "bootprofile:test.profile" {
		t.Errorf("provider should pass through on error, got %q", got)
	}
	if spec != nil {
		t.Errorf("spec should be nil on error")
	}
}

// TestResolveBootProfile_RoutesToBareAdapter is the load-bearing
// case (Open Question #2 design default):
//
//	decode the bootprofile:* id at the chat-resolve layer and
//	substitute spec.Provider (the normalized bare adapter name)
//	before the classify check fires
//
// so chat.IsCLIProvider stays narrow — it never sees the encoded
// boot-profile id.
func TestResolveBootProfile_RoutesToBareAdapter(t *testing.T) {
	root := writeCatalogForResolveTest(t)
	// Registry returns a partial-load error because test.profile's
	// slot references {{session_id}} which has no value at Reload
	// time. The registry is still usable for CompileFor; the chat
	// layer only needs the partial cache.
	reg, _ := bootprofile.NewRegistry(root)
	if reg == nil {
		t.Fatal("NewRegistry returned nil")
	}
	s := &chatServiceImpl{bootProfiles: reg}

	got, spec, err := s.resolveBootProfile("sess-X",
		"bootprofile:test.profile",
		&store.Session{}, &store.AgentProfile{Slug: "backend"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// Resolved provider should be the CLI-routable form "pty-claude"
	// (launch.provider was authored as "pty-claude"). spec.Provider
	// itself is the normalized bare adapter "claude"; the substitution
	// returns the alias form so chat.IsCLIProvider matches.
	if got != "pty-claude" {
		t.Errorf("provider = %q, want %q", got, "pty-claude")
	}
	if spec == nil {
		t.Fatal("expected non-nil spec")
	}
	if spec.Provider != "claude" {
		t.Errorf("spec.Provider = %q, want claude (normalized bare adapter)", spec.Provider)
	}
	if spec.ProviderAlias != "pty-claude" {
		t.Errorf("spec.ProviderAlias = %q, want pty-claude", spec.ProviderAlias)
	}
	// Session-scoped var must land in the rendered slot.
	if got := spec.Slots["agent"]; got != "agent for sess-X" {
		t.Errorf("slot agent = %q, want substituted session_id", got)
	}
}

// TestResolveBootProfile_StashesSpec verifies the resolver stamps
// activeSessionLaunchSpecs so driveBootSession can pick the spec up
// at boot time.
func TestResolveBootProfile_StashesSpec(t *testing.T) {
	root := writeCatalogForResolveTest(t)
	reg, _ := bootprofile.NewRegistry(root)
	if reg == nil {
		t.Fatal("NewRegistry returned nil")
	}
	s := &chatServiceImpl{bootProfiles: reg}

	_, _, err := s.resolveBootProfile("sess-42",
		"bootprofile:test.profile",
		&store.Session{}, &store.AgentProfile{Slug: "backend"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	got := s.launchSpecFor("sess-42")
	if got == nil {
		t.Fatal("expected stashed spec for sess-42")
	}
	if got.ProfileID != "test.profile" {
		t.Errorf("stashed spec ProfileID = %q, want test.profile", got.ProfileID)
	}
}

// TestResolveBootProfile_RequirementsStub is the explicit
// "scope" pin: a profile that uses a deferred slot source
// (cmd/http/role_summary/skill_index) surfaces a clean
// ErrRequirementUnsupported error rather than a half-rendered spec.
// CW-20260514-0048 deliberately defers real resolvers; a future
// ticket can replace this expectation when it lands an
// implementation.
func TestResolveBootProfile_RequirementsStub(t *testing.T) {
	root := writeCatalogForResolveTest(t)
	reg, _ := bootprofile.NewRegistry(root)
	if reg == nil {
		t.Fatal("NewRegistry returned nil")
	}
	s := &chatServiceImpl{bootProfiles: reg}

	_, spec, err := s.resolveBootProfile("sess",
		"bootprofile:deferred",
		&store.Session{}, &store.AgentProfile{Slug: "x"})
	if err == nil {
		t.Fatal("expected requirement-stub error for cmd source")
	}
	if !errors.Is(err, bootprofile.ErrRequirementUnsupported) {
		t.Fatalf("err = %v, want ErrRequirementUnsupported", err)
	}
	// Error message must name the slot, profile id, source type so
	// the operator gets a pointed diagnostic.
	msg := err.Error()
	for _, want := range []string{"deferred", "recap", "cmd"} {
		if !strings.Contains(msg, want) {
			t.Errorf("err message %q missing %q", msg, want)
		}
	}
	if spec != nil {
		t.Errorf("spec should be nil on requirement error, got %+v", spec)
	}
}

// TestResolveBootProfile_MissingProfile pins the not-in-catalog
// branch.
func TestResolveBootProfile_MissingProfile(t *testing.T) {
	root := writeCatalogForResolveTest(t)
	reg, _ := bootprofile.NewRegistry(root)
	if reg == nil {
		t.Fatal("NewRegistry returned nil")
	}
	s := &chatServiceImpl{bootProfiles: reg}

	_, _, err := s.resolveBootProfile("sess",
		"bootprofile:does.not.exist",
		nil, nil)
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if !strings.Contains(err.Error(), "does.not.exist") {
		t.Errorf("err missing profile id: %v", err)
	}
}

// TestBuildBootProfileVars_PopulatesSessionScope verifies the var
// map captures the four session-scoped keys catalog authors are
// allowed to reference. Adding to this list is a public surface
// change — the test pins the current shape so a future addition
// is deliberate.
func TestBuildBootProfileVars_PopulatesSessionScope(t *testing.T) {
	vars := buildBootProfileVars("sess-1",
		&store.Session{Provider: "bootprofile:p", Model: "claude-4"},
		&store.AgentProfile{Slug: "backend", Name: "Backend", DefaultProvider: "anthropic"})

	want := map[string]string{
		"session_id":       "sess-1",
		"session_provider": "bootprofile:p",
		"session_model":    "claude-4",
		"agent_slug":       "backend",
		"agent_name":       "Backend",
		"agent_provider":   "anthropic",
	}
	for k, v := range want {
		if got := vars[k]; got != v {
			t.Errorf("vars[%q] = %q, want %q", k, got, v)
		}
	}
}
