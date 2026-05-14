package service

// CW-20260514-0050: end-to-end smoke coverage for the boot-profile CLI
// harness. Exercises every load-bearing seam from sprint SP-20260514-0002:
//
//	0046 — bootprofile.LoadCatalog reads YAML from disk into a Catalog.
//	0046 — bootprofile.Compile (via Registry.CompileFor) renders a
//	       LaunchSpec with text + static slots fully resolved at compile
//	       time (no deferred Requirements).
//	0046 — bootprofile.ResolveRequirements drains the (here empty)
//	       Requirement list; the spec is now ready for agent.Boot.
//	0047 — bootprofile.EncodeProviderID / DecodeProviderID round-trip
//	       the dropdown wire format ("bootprofile:<profile_id>").
//	0048 — chatServiceImpl.resolveBootProfile decodes the encoded
//	       provider id, compiles fresh against the cached catalog with
//	       session-scoped vars, and stashes the result on
//	       activeSessionLaunchSpecs.
//	0048 — applyLaunchSpecToBootOpts overlays the compiled spec onto
//	       the runtimeagent.Options the chat layer would hand to
//	       agent.Boot — env / args / workdir / BootPromptOverride.
//	0048 — chat.NormalizeCLIProvider + chat.IsCLIProvider keep
//	       routing in the CLI bypass (no "Provider not available"
//	       footer) when a pty-* alias is resolved.
//	0049 — Recovery hook does NOT touch resume-flavored fields (verified
//	       elsewhere); the smoke verifies the parallel guarantee on the
//	       normal-launch path so the structural separation between
//	       normal launches and recovery-resume holds across both ends.
//
// What it does NOT do:
//
//   - Launch a real Claude / Codex / OpenCode CLI process. The smoke
//     stops at the runtimeagent.Options Boot would consume; the live
//     spawn path is exercised by the dev daemon under manual smoke
//     (see docs/boot-profile-cli-harness.md for the operator walkthrough).
//   - Round-trip through the SSE bridge. Per-session router wiring is
//     covered by chat_boot_drive_test.go; this smoke focuses on the
//     boot-profile half of the chain.
//   - Exercise the recovery / restart pre-boot hook. Recovery has its
//     own dedicated regression suite (chat_bootprofile_recovery_test.go);
//     duplicating it here would only obscure failure attribution.
//
// The example catalog lives at <repo-root>/examples/boot-profiles/ so an
// operator can point boot_profile_catalog_path at the same directory the
// test consumes — keeps the doc and the test honest against the same
// source of truth.

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/bootprofile"
	"github.com/hollis-labs/nanite/internal/chat"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// exampleCatalogDir locates examples/boot-profiles/ relative to this
// test source file so the test runs regardless of cwd. Mirrors the
// pattern used by internal/eval/runner_test.go's scenariosDir.
func exampleCatalogDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// internal/service/chat_bootprofile_smoke_test.go → ../../examples/boot-profiles
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	dir := filepath.Join(repoRoot, "examples", "boot-profiles")
	return dir
}

// TestBootProfileSmoke_CatalogLoadsAndCompiles is the load-bearing
// "does the example catalog parse + compile?" check. If a future
// change to the bootprofile schema breaks the example, this fails
// loudly — the example doubles as schema documentation, so silent
// breakage would leave operator docs lying.
func TestBootProfileSmoke_CatalogLoadsAndCompiles(t *testing.T) {
	root := exampleCatalogDir(t)
	cat, err := bootprofile.LoadCatalog(root)
	if err != nil {
		t.Fatalf("LoadCatalog(%s) = %v, want nil", root, err)
	}
	if cat.IsEmpty() {
		t.Fatalf("LoadCatalog(%s) returned empty catalog — example dropped?", root)
	}
	if _, ok := cat.Profiles["claude-smoke"]; !ok {
		t.Fatalf("profile %q missing from catalog (got %v)", "claude-smoke", catKeys(cat.Profiles))
	}
	if _, ok := cat.Launches["claude-smoke"]; !ok {
		t.Fatalf("launch %q missing from catalog (got %v)", "claude-smoke", launchKeys(cat.Launches))
	}

	// Compile with empty caller vars — this is the path
	// Registry.Reload exercises to populate the dropdown cache.
	// The example profile is authored to compile cleanly under this
	// path (slot bodies reference only identity + profile-inline vars).
	spec, err := bootprofile.CompileFromCatalog(cat, "claude-smoke", nil)
	if err != nil {
		t.Fatalf("CompileFromCatalog(empty vars) = %v, want nil (example must Reload cleanly so the dropdown surfaces it)", err)
	}
	if len(spec.Requirements) != 0 {
		t.Fatalf("spec.Requirements = %v, want empty (example must use only text + static slots)", spec.Requirements)
	}
	if spec.BootPrompt == "" {
		t.Fatal("spec.BootPrompt empty — text + static slots should render fully at compile time")
	}
	// The rendered prompt must carry both the substituted profile-inline
	// var (proves substitution fired) and the static-slot body (proves
	// disk read fired).
	if !strings.Contains(spec.BootPrompt, "Smoke Backend") {
		t.Errorf("BootPrompt missing substituted agent_label (profile.vars); got:\n%s", spec.BootPrompt)
	}
	if !strings.Contains(spec.BootPrompt, "Stay focused on the smoke task") {
		t.Errorf("BootPrompt missing static rules.md content; got:\n%s", spec.BootPrompt)
	}
}

// TestBootProfileSmoke_ResolveAndApplyPipeline drives the chat-resolve
// layer the same way chat_generate.go does on a real user turn:
//
//	encoded provider id "bootprofile:claude-smoke"
//	  → resolveBootProfile decodes + compiles + drains requirements
//	  → returns the CLI-routable form ("pty-claude") AND the compiled spec
//	  → chat.IsCLIProvider(resolved) is true so the classifier routes CLI
//	  → applyLaunchSpecToBootOpts overlays the spec onto runtimeagent.Options
//
// Assertions hit each composable piece so a regression in any of
// 0046–0048 surfaces here rather than at boot time.
func TestBootProfileSmoke_ResolveAndApplyPipeline(t *testing.T) {
	root := exampleCatalogDir(t)
	reg, err := bootprofile.NewRegistry(root)
	if err != nil {
		// NewRegistry returns partial-load errors; we don't require
		// strictness here as long as the smoke profile compiled.
		t.Logf("NewRegistry partial err (tolerated): %v", err)
	}
	if reg == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if _, ok := reg.Lookup("claude-smoke"); !ok {
		t.Fatal("claude-smoke not in cached registry after Reload")
	}

	// Confirm the dropdown round-trip: List → encoded id → Lookup.
	encoded := bootprofile.EncodeProviderID("claude-smoke")
	if encoded != "bootprofile:claude-smoke" {
		t.Fatalf("EncodeProviderID = %q, want bootprofile:claude-smoke", encoded)
	}
	decoded, ok := bootprofile.DecodeProviderID(encoded)
	if !ok || decoded != "claude-smoke" {
		t.Fatalf("DecodeProviderID(%q) = (%q, %v), want (claude-smoke, true)", encoded, decoded, ok)
	}

	s := &chatServiceImpl{bootProfiles: reg}

	// Drive the chat-resolve layer.
	resolved, spec, err := s.resolveBootProfile(
		"smoke-session-1",
		encoded,
		&store.Session{Model: "claude-opus-4-7"},
		&store.AgentProfile{Slug: "nanite-backend", Name: "Backend", DefaultProvider: "anthropic"},
	)
	if err != nil {
		t.Fatalf("resolveBootProfile = %v, want nil (example catalog has no deferred slots)", err)
	}
	if spec == nil {
		t.Fatal("resolveBootProfile returned nil spec")
	}

	// 0048 substitution: pty-claude alias survives → IsCLIProvider matches.
	if resolved != "pty-claude" {
		t.Errorf("resolved provider = %q, want pty-claude (the CLI-routable alias)", resolved)
	}
	if !chat.IsCLIProvider(resolved) {
		t.Errorf("chat.IsCLIProvider(%q) = false — routing would miss the CLI bypass", resolved)
	}

	// 0046 normalization: spec.Provider is the bare adapter; ProviderAlias
	// preserves the original launch declaration.
	if spec.Provider != "claude" {
		t.Errorf("spec.Provider = %q, want %q (NormalizeCLIProvider should strip the pty- prefix)", spec.Provider, "claude")
	}
	if spec.ProviderAlias != "pty-claude" {
		t.Errorf("spec.ProviderAlias = %q, want %q (alias is preserved verbatim)", spec.ProviderAlias, "pty-claude")
	}

	// Stash check (0048): the chat layer expects launchSpecFor to return
	// the same pointer driveBootSession would consume at boot time.
	stashed := s.launchSpecFor("smoke-session-1")
	if stashed == nil {
		t.Fatal("launchSpecFor returned nil — driveBootSession would not see the spec")
	}
	if stashed.ProfileID != "claude-smoke" {
		t.Errorf("stashed spec ProfileID = %q, want claude-smoke", stashed.ProfileID)
	}

	// Final pipeline step: apply onto the runtimeagent.Options the
	// chat layer would hand to Boot. The example launch carries env /
	// args / workdir / BootPrompt; all four must land.
	bootOpts := runtimeagent.Options{
		Mode:         runtimeagent.ModeLongLived,
		SessionID:    "smoke-session-1",
		AgentProfile: "nanite-backend",
	}
	applyLaunchSpecToBootOpts(&bootOpts, stashed)

	if bootOpts.Workdir != "/tmp/nanite-smoke-workdir" {
		t.Errorf("Workdir = %q, want /tmp/nanite-smoke-workdir (spec.Workdir should fill the empty caller Workdir)", bootOpts.Workdir)
	}
	if got := bootOpts.Env["NANITE_SMOKE"]; got != "1" {
		t.Errorf("Env[NANITE_SMOKE] = %q, want 1 (spec.Env should overlay)", got)
	}
	if !containsArgsSlice(bootOpts.ExtraArgs, "--add-dir", "/tmp/nanite-smoke-workdir") {
		t.Errorf("ExtraArgs = %v, missing --add-dir /tmp/nanite-smoke-workdir from spec.Args", bootOpts.ExtraArgs)
	}
	if bootOpts.BootPromptOverride == "" {
		t.Error("BootPromptOverride empty — spec.BootPrompt should override the legacy composeSystemPrompt path")
	}
	// The rendered prompt carries both the profile-inline var
	// (substituted by the compiler) AND the static rules.md body
	// (read from disk relative to the catalog root).
	if !strings.Contains(bootOpts.BootPromptOverride, "Smoke Backend") {
		t.Errorf("BootPromptOverride missing agent_label substitution; got:\n%s", bootOpts.BootPromptOverride)
	}
	if !strings.Contains(bootOpts.BootPromptOverride, "Stay focused on the smoke task") {
		t.Errorf("BootPromptOverride missing static rules.md content; got:\n%s", bootOpts.BootPromptOverride)
	}

	// "Normal launches never carry resume fields" — pinned across the
	// resolve+apply path. Recovery has its own pin in
	// chat_bootprofile_recovery_test.go; this is the smoke parallel.
	if bootOpts.Mode != runtimeagent.ModeLongLived {
		t.Errorf("Mode = %v, want ModeLongLived (smoke must not flip Mode to ModeResume)", bootOpts.Mode)
	}
	if bootOpts.ResumeFromCheckpoint != "" {
		t.Errorf("ResumeFromCheckpoint = %q, want empty (no-resume guarantee on normal launches)", bootOpts.ResumeFromCheckpoint)
	}
	if bootOpts.IsRelaunch {
		t.Error("IsRelaunch true after normal-launch apply, want false")
	}
}

// TestBootProfileSmoke_RegistryListSurface verifies the dropdown
// surface (Registry.List) carries the smoke profile under its
// encoded id. The handler in internal/api/providers.go iterates List()
// to build the synthetic provider rows; this asserts the iteration
// would find the smoke profile.
func TestBootProfileSmoke_RegistryListSurface(t *testing.T) {
	root := exampleCatalogDir(t)
	reg, _ := bootprofile.NewRegistry(root)
	if reg == nil {
		t.Fatal("NewRegistry returned nil")
	}

	specs := reg.List()
	if len(specs) == 0 {
		t.Fatal("Registry.List() empty — dropdown would not show the smoke profile")
	}
	var found *bootprofile.LaunchSpec
	for _, s := range specs {
		if s.ProfileID == "claude-smoke" {
			found = s
			break
		}
	}
	if found == nil {
		t.Fatalf("claude-smoke missing from Registry.List(); got %d entries", len(specs))
	}
	if found.UILabel != "Claude CLI (smoke)" {
		t.Errorf("UILabel = %q, want %q (launch.ui_label takes precedence)", found.UILabel, "Claude CLI (smoke)")
	}
}

// catKeys / launchKeys produce a stable sorted key listing for test
// error messages. Keeping them local keeps the file dependency-light.
func catKeys(m map[string]bootprofile.Profile) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func launchKeys(m map[string]bootprofile.Launch) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// containsArgsSlice returns true iff the given args contain the
// given subsequence in order. Used to assert spec.Args landed in
// ExtraArgs verbatim (order matters for --flag value pairs).
func containsArgsSlice(haystack []string, needle ...string) bool {
	if len(needle) == 0 || len(haystack) < len(needle) {
		return false
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		match := true
		for j, want := range needle {
			if haystack[i+j] != want {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
