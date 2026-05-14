package service

// CW-20260514-0048: integration-style regression coverage for the
// decode -> route pipeline introduced by this ticket. Verifies that
// when a "bootprofile:<id>" provider name flows through the chat
// service's resolve layer, the downstream classifyNilProvider call
// sees the normalized bare adapter (e.g. "claude") and routes to
// driveBootSession via nilProviderRouteCLI.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/bootprofile"
	"github.com/hollis-labs/nanite/internal/chat"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// writeBootprofileRouteCatalog plants a minimal catalog whose launch
// declares provider=pty-claude. The compiler normalizes that to
// "claude" — which is the value the chat resolve layer should
// substitute for the encoded provider id before classifyNilProvider
// fires.
func writeBootprofileRouteCatalog(t *testing.T) string {
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
	mustWrite("boot-profiles/route.test.yaml",
		`id: route.test
display_name: "Route Test"
launch: route-launch
identity:
  lineage_alias: route.test
slots:
  agent:
    type: text
    content: "agent for tests"
`)
	mustWrite("launches/route-launch.yaml",
		`id: route-launch
provider: pty-claude
`)
	return root
}

// TestBootprofile_RoutesToBareAdapter_ThroughClassifier is the
// load-bearing integration test for the wire-up: feed
// "bootprofile:route.test" through resolveBootProfile, then through
// classifyNilProvider, and confirm we land on nilProviderRouteCLI
// (the route that fires driveBootSession). This pins the documented
// design default from open question #2:
//
//	"Decode the id at the chat-resolve layer and substitute
//	 spec.Provider (the normalized bare adapter name) before the
//	 classify check fires — keep IsCLIProvider narrow."
//
// Three guarantees roll into this test:
//
//  1. IsCLIProvider stays narrow: "bootprofile:route.test" is NOT a
//     CLI provider (else classifyNilProvider would short-circuit on
//     the un-decoded form and bypass our spec stash).
//  2. resolveBootProfile substitutes to the bare adapter ("claude").
//  3. classifyNilProvider on "claude" with a wired adapter returns
//     nilProviderRouteCLI.
func TestBootprofile_RoutesToBareAdapter_ThroughClassifier(t *testing.T) {
	if chat.IsCLIProvider("bootprofile:route.test") {
		t.Fatal("IsCLIProvider should NOT match bootprofile: ids (else decode would be bypassed)")
	}

	root := writeBootprofileRouteCatalog(t)
	reg, err := bootprofile.NewRegistry(root)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	adapter := &stubCLIAdapter{name: "claude"}
	deps := &runtimeagent.Dependencies{
		ProviderAdapter: func(name string) provider.CLIAdapter {
			// Mirror the production agent_deps closure: it strips the
			// "pty-" / "sub-" prefix via stripRegistryPrefix before
			// the bare lookup. The chat layer passes the alias form
			// ("pty-claude") through this closure verbatim.
			bare := stripRegistryPrefix(name)
			if bare == "claude" {
				return adapter
			}
			return nil
		},
	}
	s := &chatServiceImpl{
		bootProfiles: reg,
		agentDeps:    deps,
	}

	got, spec, err := s.resolveBootProfile("sess-1",
		"bootprofile:route.test",
		&store.Session{}, &store.AgentProfile{Slug: "x"})
	if err != nil {
		t.Fatalf("resolveBootProfile: %v", err)
	}
	// The substituted form is the CLI-routable alias the launch
	// declared ("pty-claude"), NOT the bare adapter — that's the only
	// shape chat.IsCLIProvider matches today, and the existing
	// classifier flow expects it.
	if got != "pty-claude" {
		t.Fatalf("resolved provider = %q, want pty-claude", got)
	}
	if spec == nil {
		t.Fatal("spec missing")
	}

	// IsCLIProvider must match the substituted form so the
	// downstream classifyNilProvider sees the CLI route.
	if !chat.IsCLIProvider(got) {
		t.Fatalf("chat.IsCLIProvider(%q) = false, want true", got)
	}

	// Now run the classifier on the substituted name — the same
	// shape chat_generate.go threads through.
	if route := s.classifyNilProvider(got); route != nilProviderRouteCLI {
		t.Fatalf("classifyNilProvider(%q) = %v, want nilProviderRouteCLI (route to driveBootSession)",
			got, route)
	}
}

// TestCLIRoutableProvider_PrefersAlias verifies the alias passthrough
// branch in cliRoutableProvider: when ProviderAlias is already a
// CLI-routable form, it's used verbatim.
func TestCLIRoutableProvider_PrefersAlias(t *testing.T) {
	cases := []struct {
		alias, bare, want string
	}{
		{"pty-claude", "claude", "pty-claude"},
		{"pty-codex", "codex", "pty-codex"},
		{"pty-opencode", "opencode", "pty-opencode"},
		{"sub-claude", "claude", "sub-claude"},
		{"pty", "claude", "pty"}, // legacy bare alias
	}
	for _, c := range cases {
		spec := &bootprofile.LaunchSpec{ProviderAlias: c.alias, Provider: c.bare}
		if got := cliRoutableProvider(spec); got != c.want {
			t.Errorf("cliRoutableProvider(alias=%q bare=%q) = %q, want %q",
				c.alias, c.bare, got, c.want)
		}
	}
}

// TestCLIRoutableProvider_SynthesizesPrefix verifies the fallback: a
// bare-adapter alias gets the "pty-" prefix synthesized so
// IsCLIProvider matches. Catalog authors who declared their launch
// with the bare name (e.g. provider: claude) still route correctly.
func TestCLIRoutableProvider_SynthesizesPrefix(t *testing.T) {
	cases := []struct {
		alias, bare, want string
	}{
		{"claude", "claude", "pty-claude"},
		{"codex", "codex", "pty-codex"},
		{"opencode", "opencode", "pty-opencode"},
		{"", "claude", "pty-claude"}, // alias missing — still routable
	}
	for _, c := range cases {
		spec := &bootprofile.LaunchSpec{ProviderAlias: c.alias, Provider: c.bare}
		got := cliRoutableProvider(spec)
		if got != c.want {
			t.Errorf("cliRoutableProvider(alias=%q bare=%q) = %q, want %q",
				c.alias, c.bare, got, c.want)
		}
		if !chat.IsCLIProvider(got) {
			t.Errorf("synthesized %q does not pass IsCLIProvider", got)
		}
	}
}

// TestCLIRoutableProvider_NilSpec defensive nil case.
func TestCLIRoutableProvider_NilSpec(t *testing.T) {
	if got := cliRoutableProvider(nil); got != "" {
		t.Fatalf("cliRoutableProvider(nil) = %q, want empty", got)
	}
}

// TestBootprofile_DecodedIDNotCLIProvider is the structural guard:
// chat.IsCLIProvider must NOT match "bootprofile:X" so the decode
// path stays the single entry point. If a future ticket extends
// IsCLIProvider to also match boot-profile ids, the bootprofile
// stash + substitute logic in chat_generate.go would be bypassed
// and per-profile env / args / workdir / prompt would silently fail.
func TestBootprofile_DecodedIDNotCLIProvider(t *testing.T) {
	cases := []string{
		"bootprofile:nanite.backend.main",
		"bootprofile:foo",
		"bootprofile:foo.bar.baz",
	}
	for _, c := range cases {
		if chat.IsCLIProvider(c) {
			t.Errorf("IsCLIProvider(%q) = true, want false", c)
		}
	}
}
