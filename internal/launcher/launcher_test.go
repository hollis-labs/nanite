package launcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hollis-labs/go-agent-launch/agentlaunch"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
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
	if plan.BootProfile.Inline.BootPrompt != spec.BootPrompt {
		t.Error("inline boot prompt does not match compiled spec boot prompt")
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
