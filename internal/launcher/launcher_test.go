package launcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hollis-labs/agentkit/agentlaunch"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"

	"github.com/hollis-labs/nanite/internal/agentregistry"
)

// writeCatalog materializes a minimal boot-profile catalog under root and
// returns the catalog root path. The profile compiles with only text
// slots so the compile path needs no filesystem statics and no deferred
// resolvers — it is a clean compile-level fixture.
func writeCatalog(t *testing.T, provider string) string {
	t.Helper()
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "boot-profiles"))
	mustMkdir(t, filepath.Join(root, "launches"))

	profile := `id: test-profile
display_name: "Launcher Test Profile"
launch: test-launch
identity:
  lineage_alias: launcher-test
  role: backend
  project: nanite
  work_root: ` + filepath.Join(root, "work") + `
slots:
  agent:
    type: text
    content: |
      You are the launcher test agent for {{project}}.
`
	mustWrite(t, filepath.Join(root, "boot-profiles", "test-profile.yaml"), profile)

	launch := `id: test-launch
provider: ` + provider + `
workdir: ` + filepath.Join(root, "work") + `
ui_label: "Launcher Test"
env:
  LAUNCHER_TEST: "1"
boot_mode: ""
`
	mustWrite(t, filepath.Join(root, "launches", "test-launch.yaml"), launch)
	return root
}

// writePromptOnlyCatalog materializes a catalog whose profile has no
// launch — a prompt-only profile that must be rejected by Plan.
func writePromptOnlyCatalog(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "boot-profiles"))
	profile := `id: prompt-only
display_name: "Prompt Only"
identity:
  lineage_alias: prompt-only
slots:
  agent:
    type: text
    content: "prompt only, no launch target"
`
	mustWrite(t, filepath.Join(root, "boot-profiles", "prompt-only.yaml"), profile)
	return root
}

// writeCmdSentinelCatalog materializes a catalog whose profile carries a
// deferred `cmd` slot whose command `touch`es a sentinel file. A test
// can then assert whether requirement resolution ran by checking for the
// sentinel's existence. withLaunch controls whether the profile is
// launchable (has a provider) — false makes it prompt-only.
//
// Returns the catalog root and the sentinel path the cmd would create.
func writeCmdSentinelCatalog(t *testing.T, withLaunch bool) (root, sentinel string) {
	t.Helper()
	root = t.TempDir()
	sentinel = filepath.Join(root, "cmd-resolver-ran.sentinel")
	mustMkdir(t, filepath.Join(root, "boot-profiles"))
	mustMkdir(t, filepath.Join(root, "launches"))

	launchLine := ""
	if withLaunch {
		launchLine = "launch: sentinel-launch\n"
	}
	profile := `id: sentinel-profile
display_name: "Sentinel Profile"
` + launchLine + `identity:
  lineage_alias: sentinel-test
  role: backend
  project: nanite
slots:
  agent:
    type: cmd
    run: "touch ` + sentinel + `"
`
	mustWrite(t, filepath.Join(root, "boot-profiles", "sentinel-profile.yaml"), profile)

	if withLaunch {
		launch := `id: sentinel-launch
provider: claude
workdir: ` + filepath.Join(root, "work") + `
ui_label: "Sentinel"
boot_mode: ""
`
		mustWrite(t, filepath.Join(root, "launches", "sentinel-launch.yaml"), launch)
	}
	return root, sentinel
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestPlan_CompilesAndValidates is the compile-level acceptance check:
// a local launch profile compiles, resolves, and projects onto a valid
// shared agentlaunch.LaunchPlan.
func TestPlan_CompilesAndValidates(t *testing.T) {
	root := writeCatalog(t, "claude")

	spec, plan, err := Plan(context.Background(), Config{
		CatalogPath: root,
		Profile:     "test-profile",
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if spec.Provider != "claude" {
		t.Errorf("spec.Provider = %q, want claude", spec.Provider)
	}
	if spec.BootPrompt == "" {
		t.Error("spec.BootPrompt is empty — text slot should have rendered")
	}
	if plan.Provider.ID != "claude" {
		t.Errorf("plan.Provider.ID = %q, want claude", plan.Provider.ID)
	}
	if plan.Project.ID != "nanite" {
		t.Errorf("plan.Project.ID = %q, want nanite", plan.Project.ID)
	}
	if plan.Runtime != agentlaunch.RuntimeStreamingStdio {
		t.Errorf("plan.Runtime = %q, want streaming-stdio", plan.Runtime)
	}
	if err := plan.Validate(); err != nil {
		t.Errorf("plan.Validate: %v", err)
	}
	if plan.BootProfile.Inline == nil {
		t.Fatal("plan.BootProfile.Inline is nil — boot profile should be carried inline")
	}
	// PlanFromLaunch carries the rendered boot body as BootContent (the
	// per-task kickoff body), not BootPrompt.
	if plan.BootProfile.Inline.BootContent != spec.BootPrompt {
		t.Error("inline boot content does not match compiled spec boot prompt")
	}
}

// TestPlan_PromptOnlyProfileRejected confirms a profile with no launch
// provider is rejected with a pointed error wrapping ErrMissingProviderID.
func TestPlan_PromptOnlyProfileRejected(t *testing.T) {
	root := writePromptOnlyCatalog(t)

	_, _, err := Plan(context.Background(), Config{
		CatalogPath: root,
		Profile:     "prompt-only",
	})
	if err == nil {
		t.Fatal("Plan: expected error for prompt-only profile, got nil")
	}
	if !errors.Is(err, agentlaunch.ErrMissingProviderID) {
		t.Errorf("Plan error = %v, want wrap of ErrMissingProviderID", err)
	}
}

// TestPlan_MissingProfile confirms an unknown profile id surfaces a
// catalog lookup error rather than a panic.
func TestPlan_MissingProfile(t *testing.T) {
	root := writeCatalog(t, "claude")
	_, _, err := Plan(context.Background(), Config{
		CatalogPath: root,
		Profile:     "no-such-profile",
	})
	if err == nil {
		t.Fatal("Plan: expected error for unknown profile, got nil")
	}
}

// TestPlan_RequiredFields confirms empty CatalogPath / Profile fail fast.
func TestPlan_RequiredFields(t *testing.T) {
	if _, _, err := Plan(context.Background(), Config{Profile: "x"}); err == nil {
		t.Error("Plan: expected error for empty CatalogPath")
	}
	if _, _, err := Plan(context.Background(), Config{CatalogPath: "/tmp"}); err == nil {
		t.Error("Plan: expected error for empty Profile")
	}
}

// fakeCLIAdapter is a minimal CLIAdapter whose binary is /usr/bin/true so
// agent.Boot can run the full pipeline (workspace + bootdir + manager
// Start) end-to-end without a real provider CLI installed. The spawned
// process exits 0 immediately.
type fakeCLIAdapter struct{ name string }

func (f *fakeCLIAdapter) Name() string { return f.name }
func (f *fakeCLIAdapter) BuildArgs(prompt, system, sessID string) []string {
	return []string{"--prompt", prompt}
}
func (f *fakeCLIAdapter) ParseLine([]byte) ([]llmtypes.StreamEvent, error) { return nil, nil }
func (f *fakeCLIAdapter) Detect() (string, bool)                           { return "/usr/bin/true", true }

// TestLaunch_FakeRuntime exercises the full standalone launch pipeline
// (Plan → buildDeps → agent.Boot → Wait) against a fake claude adapter
// whose binary is /usr/bin/true. No real provider CLI, no store — the
// in-memory runtime store is used. This is the fake-runtime verification
// the CW-0027 task accepts in lieu of a live provider launch.
func TestLaunch_FakeRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake runtime relies on /usr/bin/true")
	}
	if _, err := os.Stat("/usr/bin/true"); err != nil {
		t.Skipf("/usr/bin/true unavailable: %v", err)
	}
	root := writeCatalog(t, "claude")

	res, err := Launch(context.Background(), Config{
		CatalogPath:    root,
		Profile:        "test-profile",
		CLIAdapters:    []provider.CLIAdapter{&fakeCLIAdapter{name: "claude"}},
		WorkspacesRoot: t.TempDir(),
		Wait:           true,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if res.SessionID == "" {
		t.Error("Result.SessionID is empty")
	}
	if res.Provider != "claude" {
		t.Errorf("Result.Provider = %q, want claude", res.Provider)
	}
	if res.BootDir == "" {
		t.Error("Result.BootDir is empty")
	}
	if res.Spec == nil || res.Spec.BootPrompt == "" {
		t.Error("Result.Spec / boot prompt missing")
	}
	if err := res.Plan.Validate(); err != nil {
		t.Errorf("Result.Plan invalid: %v", err)
	}
}

// TestLaunch_NoAdapters confirms Launch fails clearly when no CLI
// adapters are supplied.
func TestLaunch_NoAdapters(t *testing.T) {
	root := writeCatalog(t, "claude")
	_, err := Launch(context.Background(), Config{
		CatalogPath: root,
		Profile:     "test-profile",
	})
	if err == nil {
		t.Fatal("Launch: expected error when CLIAdapters is empty")
	}
}

// TestPlan_NoProviderPrecheckSkipsResolvers proves the side-effect-free
// precheck (Copilot round-1 #2): a prompt-only profile carrying a
// deferred `cmd` slot must be rejected BEFORE the cmd resolver runs, so
// `nanite launch --dry-run` on a non-launchable profile performs no side
// effects. The cmd would `touch` a sentinel; the sentinel must NOT exist.
func TestPlan_NoProviderPrecheckSkipsResolvers(t *testing.T) {
	root, sentinel := writeCmdSentinelCatalog(t, false /* prompt-only */)

	_, _, err := Plan(context.Background(), Config{
		CatalogPath: root,
		Profile:     "sentinel-profile",
	})
	if err == nil {
		t.Fatal("Plan: expected rejection for prompt-only profile, got nil")
	}
	if !errors.Is(err, agentlaunch.ErrMissingProviderID) {
		t.Errorf("Plan error = %v, want wrap of ErrMissingProviderID", err)
	}
	if _, statErr := os.Stat(sentinel); statErr == nil {
		t.Error("cmd resolver ran (sentinel created) — precheck must reject BEFORE deferred resolution")
	}
}

// TestLaunch_NoAdaptersPrecheckSkipsResolvers proves the side-effect-free
// launch precheck (Copilot round-1 #3): Launch must reject a missing
// CLIAdapters BEFORE Plan runs the deferred cmd/http resolvers. The
// launchable profile carries a `cmd` slot; with no adapters supplied the
// sentinel must NOT be created.
func TestLaunch_NoAdaptersPrecheckSkipsResolvers(t *testing.T) {
	root, sentinel := writeCmdSentinelCatalog(t, true /* launchable */)

	_, err := Launch(context.Background(), Config{
		CatalogPath: root,
		Profile:     "sentinel-profile",
		// CLIAdapters deliberately empty.
	})
	if err == nil {
		t.Fatal("Launch: expected error when CLIAdapters is empty")
	}
	if _, statErr := os.Stat(sentinel); statErr == nil {
		t.Error("cmd resolver ran (sentinel created) — Launch must reject missing adapters BEFORE Plan/resolution")
	}
}

// TestBootOptionsFor confirms the LaunchSpec → agent.Options projection
// carries the load-bearing fields.
func TestBootOptionsFor(t *testing.T) {
	root := writeCatalog(t, "claude")
	spec, _, err := Plan(context.Background(), Config{CatalogPath: root, Profile: "test-profile"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	opts := bootOptionsFor(spec)
	if opts.Provider != "claude" {
		t.Errorf("opts.Provider = %q, want claude", opts.Provider)
	}
	if opts.BootPromptOverride != spec.BootPrompt {
		t.Error("opts.BootPromptOverride should equal spec.BootPrompt")
	}
	if opts.Env["LAUNCHER_TEST"] != "1" {
		t.Errorf("opts.Env[LAUNCHER_TEST] = %q, want 1", opts.Env["LAUNCHER_TEST"])
	}
	if opts.SessionMeta["launch_source"] != "standalone-launcher" {
		t.Errorf("opts.SessionMeta[launch_source] = %v, want standalone-launcher", opts.SessionMeta["launch_source"])
	}
}

// TestBuildDeps_WorkspacesRootFallback verifies buildDeps resolves an
// empty Config.WorkspacesRoot to ~/.nanite/workspaces — agent.Boot's
// workspaceCreate hard-errors on an empty root, so the launcher must
// default it (CW-20260515-0028 live-launch acceptance gap fix).
func TestBuildDeps_WorkspacesRootFallback(t *testing.T) {
	deps, err := buildDeps(Config{})
	if err != nil {
		t.Fatalf("buildDeps: %v", err)
	}
	if deps.WorkspacesRoot == "" {
		t.Fatal("buildDeps: WorkspacesRoot should be defaulted, not empty")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	want := filepath.Join(home, ".nanite", "workspaces")
	if deps.WorkspacesRoot != want {
		t.Errorf("buildDeps WorkspacesRoot = %q, want %q", deps.WorkspacesRoot, want)
	}

	// An explicit WorkspacesRoot is honored verbatim.
	explicit := t.TempDir()
	deps2, err := buildDeps(Config{WorkspacesRoot: explicit})
	if err != nil {
		t.Fatalf("buildDeps (explicit): %v", err)
	}
	if deps2.WorkspacesRoot != explicit {
		t.Errorf("buildDeps WorkspacesRoot = %q, want explicit %q", deps2.WorkspacesRoot, explicit)
	}
}

// TestD1_OfflineStandaloneLaunch is the S5 design-lock D1 regression: the
// standalone launcher's Plan path must work with NO Tether process and NO
// provider registry in the loop — a fully offline launch.
//
// D1 (EP-20260516-0001 S5 cutover): `nanite launch` is a self-contained
// operator entry point. It loads a catalog off disk, compiles the profile
// through the bootprofile compiler, and projects + validates a shared
// agentlaunch.LaunchPlan — none of which may depend on a running Tether
// orchestrator, an HTTP registry, or a network round-trip. Plan() is the
// no-side-effects half of the pipeline (see launcher.go's Plan doc), so a
// pure-offline assertion is cleanly doable as a NORMAL (non-smoke) test —
// it needs no real provider CLI and spawns no subprocess. This is the
// design-lock pin: if a future change makes Plan reach out to a registry
// or a Tether endpoint, this test breaks.
//
// The test deliberately uses a catalog whose only slots are `text`
// (compile-time, no resolver) so Plan performs ZERO side effects at all —
// no command execution, no file statics, no network. The full Plan
// pipeline (LoadCatalog → CompileFromCatalog → ResolveRequirements →
// ToLaunchPlan → Validate) runs entirely against the local filesystem.
func TestD1_OfflineStandaloneLaunch(t *testing.T) {
	root := writeCatalog(t, "claude")

	// Plan runs the entire compile + project + validate pipeline. No
	// Config.Store, no CLIAdapters, no BinaryPath/DBPath — there is
	// nothing here that could reach a Tether process or a registry.
	spec, plan, err := Plan(context.Background(), Config{
		CatalogPath: root,
		Profile:     "test-profile",
	})
	if err != nil {
		t.Fatalf("D1: offline Plan failed: %v", err)
	}

	// The compiled spec must be fully resolved offline — a launchable
	// provider and a rendered boot prompt, with no deferred Requirements
	// left to drain (the text-only catalog resolves entirely at compile
	// time, so the offline path produces a complete, boot-ready spec).
	if spec.Provider != "claude" {
		t.Errorf("D1: spec.Provider = %q, want claude", spec.Provider)
	}
	if spec.BootPrompt == "" {
		t.Error("D1: spec.BootPrompt is empty — the offline compile produced no boot prompt")
	}
	if len(spec.Requirements) != 0 {
		t.Errorf("D1: spec.Requirements = %v, want empty — a text-only catalog must resolve fully offline", spec.Requirements)
	}

	// The shared LaunchPlan must validate offline. Validate is the
	// shared-plan convergence gate (provider×runtime matrix lookup, path
	// expansion, sentinel errors) — all of it pure, no Tether, no network.
	if err := plan.Validate(); err != nil {
		t.Errorf("D1: offline LaunchPlan.Validate failed: %v", err)
	}
	if plan.BootProfile.Inline == nil {
		t.Fatal("D1: plan.BootProfile.Inline is nil — the offline plan carries no inline boot profile")
	}
	if plan.BootProfile.Inline.BootContent != spec.BootPrompt {
		t.Error("D1: inline boot content diverged from the compiled spec on the offline path")
	}
}

// TestC2_RegistryPrimaryPlanThroughPlanFromLaunch is the C2 acceptance
// check: a launch resolved with a wired registry must drive the shipped
// agentlaunch.PlanFromLaunch bridge and produce a Validate()-clean
// LaunchPlan.
//
// The registry here has no providers/ entries, so the runtime binding
// degrades to the spec/profile fallback — but the plan still flows
// through PlanFromLaunch (NOT the retired hand-rolled ToLaunchPlan).
func TestC2_RegistryPrimaryPlanThroughPlanFromLaunch(t *testing.T) {
	root := writeCatalog(t, "claude")
	reg := agentregistry.Build(root, "", nil)

	spec, plan, err := Plan(context.Background(), Config{
		CatalogPath: root,
		Profile:     "test-profile",
		Registry:    reg,
	})
	if err != nil {
		t.Fatalf("C2: registry-primary Plan failed: %v", err)
	}
	if err := plan.Validate(); err != nil {
		t.Errorf("C2: PlanFromLaunch plan failed Validate: %v", err)
	}
	if plan.Provider.ID != "claude" {
		t.Errorf("C2: plan.Provider.ID = %q, want claude", plan.Provider.ID)
	}
	if plan.Runtime != agentlaunch.RuntimeStreamingStdio {
		t.Errorf("C2: plan.Runtime = %q, want streaming-stdio", plan.Runtime)
	}
	// PlanFromLaunch carries the rendered boot body inline as BootContent.
	if plan.BootProfile.Inline == nil || plan.BootProfile.Inline.BootContent != spec.BootPrompt {
		t.Error("C2: PlanFromLaunch plan does not carry the rendered boot body inline")
	}
	// §4.2 — the agent identity is resolved caller-side onto the plan.
	if plan.Agent.ID == "" {
		t.Error("C2: plan.Agent.ID is empty — agent identity must be caller-resolved")
	}
	// PlanFromLaunch stamps launch provenance onto Metadata.Annotations.
	if plan.Metadata.Annotations["agentlaunch.launch_spec"] == "" {
		t.Error("C2: plan metadata missing the PlanFromLaunch launch_spec annotation")
	}
}

// TestC2_RegistryDownDegradesToFileBacked is the D1 regression for C2:
// registry-primary launch resolution must DEGRADE cleanly to the
// file-backed / spec fallback when the registry is unavailable. The
// launch still produces a valid plan; it never hard-fails on a down
// registry.
func TestC2_RegistryDownDegradesToFileBacked(t *testing.T) {
	root := writeCatalog(t, "claude")
	// A registry whose inner registrar is permanently down, fronted by a
	// DegradingRegistrar with an empty cache — every query is a cache
	// miss, the D1 degrade-to-fallback condition.
	reg := agentregistry.NewForTest(downRegistrarStub{}, root)

	spec, plan, err := Plan(context.Background(), Config{
		CatalogPath: root,
		Profile:     "test-profile",
		Registry:    reg,
	})
	if err != nil {
		t.Fatalf("D1/C2: launch with a down registry hard-failed (must degrade): %v", err)
	}
	if err := plan.Validate(); err != nil {
		t.Errorf("D1/C2: degraded plan failed Validate: %v", err)
	}
	if plan.Provider.ID != spec.Provider {
		t.Errorf("D1/C2: degraded plan provider = %q, want spec fallback %q", plan.Provider.ID, spec.Provider)
	}
}

// downRegistrarStub is a Registrar whose every call fails — it
// simulates an unreachable directory for the C2 D1 degrade test.
type downRegistrarStub struct{}

func (downRegistrarStub) Handle(agentlaunch.RegistryEnvelope) (agentlaunch.RegistryResponse, error) {
	return agentlaunch.RegistryResponse{}, errors.New("directory unreachable")
}
