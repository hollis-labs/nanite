package service

// chat_boot_drive_launch_plan_test.go — S5 Phase F regression coverage.
//
// Phase F flips the GUI chat launch path so a boot-profile chat session
// resolves its runtime binding registry-primary and assembles its plan
// via agentlaunch.PlanFromLaunch — the SAME shared launchplan.Build seam
// the standalone launcher uses. These tests prove:
//
//   - the chat launch path resolves registry-primary (a wired registry
//     drives the resolution) AND degrades cleanly to the composer/
//     profile-default RuntimeBinding when the registry is unavailable;
//   - a chat session's assembled plan goes through PlanFromLaunch to a
//     Validate()-clean agentlaunch.LaunchPlan;
//   - the composer/profile provider selection stays authoritative — the
//     projected bootOpts carries exactly the spec.Provider the user
//     picked, identical to the pre-Phase-F spec-direct overlay;
//   - chat UX/behavior is unchanged: the plan path projects the same
//     Provider / Workdir / Env / BootPrompt / ExtraArgs values the
//     legacy applyLaunchSpecToBootOpts produced.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/go-agent-launch/agentlaunch"
	"gopkg.in/yaml.v3"

	"github.com/hollis-labs/nanite/internal/agentregistry"
	"github.com/hollis-labs/nanite/internal/bootprofile"
	"github.com/hollis-labs/nanite/internal/launchplan"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
)

// chatLaunchSpecFixture is a compiled-shape bootprofile.LaunchSpec
// representing what the bootprofile compiler hands driveBootSession after
// a composer dropdown selection. The Provider field IS the composer's
// provider pick — locked decision §4.1 keeps it authoritative.
func chatLaunchSpecFixture() *bootprofile.LaunchSpec {
	return &bootprofile.LaunchSpec{
		ProfileID:  "chat-smoke",
		LaunchID:   "chat-smoke-launch",
		Provider:   "claude",
		UILabel:    "Chat Smoke",
		BootPrompt: "You are a chat smoke-test agent.",
		Workdir:    "/tmp/chat-smoke-wd",
		Env:        map[string]string{"PROFILE_KEY": "profile-value"},
		Args:       []string{"--profile-arg"},
		Identity: bootprofile.Identity{
			ProfileID: "chat-smoke",
		},
	}
}

// writeChatRuntimeBindingCatalog materializes a minimal boot-profile
// catalog with one providers/ entry that ingests as a runtime-binding
// record under runnerID. Mirrors agentregistry's own test fixture so the
// chat path can exercise a genuine registry-primary resolution.
func writeChatRuntimeBindingCatalog(t *testing.T, runnerID string) string {
	t.Helper()
	root := t.TempDir()
	provDir := filepath.Join(root, "providers")
	if err := os.MkdirAll(provDir, 0o755); err != nil {
		t.Fatalf("mkdir providers: %v", err)
	}
	contract := agentlaunch.RuntimeBindingContract{
		Meta: agentlaunch.RegistryContractMeta{
			Ref: agentlaunch.RegistryObjectRef{
				Kind: agentlaunch.RegistryKindRuntimeBinding,
				Name: runnerID,
			},
			SchemaVersion: agentlaunch.RuntimeBindingSchemaVersionV1,
			Interface:     agentlaunch.RuntimeBindingInterfaceV1,
		},
		Binding: agentlaunch.RuntimeBinding{
			Provider:    runnerID,
			RuntimeKind: agentlaunch.RuntimeStreamingStdio,
		},
	}
	raw, err := yaml.Marshal(contract)
	if err != nil {
		t.Fatalf("marshal contract: %v", err)
	}
	body := "id: " + runnerID + "\n" + string(raw)
	if err := os.WriteFile(filepath.Join(provDir, runnerID+".yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write provider: %v", err)
	}
	return root
}

// downRegistrarStub is a Registrar whose every call fails — it simulates
// an unreachable directory so the chat path's D1 degrade-to-fallback
// branch can be exercised without standing up a full catalog.
type downRegistrarStub struct{}

func (downRegistrarStub) Handle(agentlaunch.RegistryEnvelope) (agentlaunch.RegistryResponse, error) {
	return agentlaunch.RegistryResponse{}, errors.New("directory unreachable")
}

// TestPhaseF_ChatLaunchResolvesRegistryPrimary proves the GUI chat
// launch path resolves its runtime binding registry-primary: with a
// wired registry that has a runtime-binding for the runner, the chat
// path's launchplan.Build assembles a Validate()-clean plan whose
// provider came through the registrar.
func TestPhaseF_ChatLaunchResolvesRegistryPrimary(t *testing.T) {
	root := writeChatRuntimeBindingCatalog(t, "claude")
	reg := agentregistry.Build(root, "", nil)

	s := &chatServiceImpl{agentRegistry: reg}
	spec := chatLaunchSpecFixture()

	// launchplan.Build is the seam driveBootSession routes through. A
	// successful Build with a registry-primary resolution proves the
	// chat path resolves through the registrar.
	plan, err := launchplan.Build(spec, s.agentRegistry, nil)
	if err != nil {
		t.Fatalf("Phase F: registry-primary chat Build failed: %v", err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("Phase F: registry-primary chat plan failed Validate: %v", err)
	}
	if plan.Provider.ID != "claude" {
		t.Errorf("Phase F: plan.Provider.ID = %q, want claude (registry-primary)", plan.Provider.ID)
	}
	if plan.Runtime != agentlaunch.RuntimeStreamingStdio {
		t.Errorf("Phase F: plan.Runtime = %q, want streaming-stdio", plan.Runtime)
	}

	// End-to-end: the boot-opts projection the chat path performs lands
	// the registry-resolved provider on Options.
	bootOpts := &runtimeagent.Options{Mode: runtimeagent.ModeLongLived}
	s.applyLaunchSpecAsPlanToBootOpts(bootOpts, spec)
	if bootOpts.Provider != "claude" {
		t.Errorf("Phase F: bootOpts.Provider = %q, want claude (composer pick, registry-resolved)", bootOpts.Provider)
	}
}

// TestPhaseF_ChatLaunchDegradesWhenRegistryDown is the D1 regression for
// the chat path: when the directory registry is unavailable, the chat
// launch path must DEGRADE cleanly to the composer/profile-default
// RuntimeBinding — it never hard-fails. The user still gets their
// composer-selected provider.
func TestPhaseF_ChatLaunchDegradesWhenRegistryDown(t *testing.T) {
	root := writeChatRuntimeBindingCatalog(t, "claude")
	// A registry whose inner registrar is permanently down, fronted by a
	// DegradingRegistrar with an empty cache — every query is a cache
	// miss, the D1 degrade-to-fallback condition.
	reg := agentregistry.NewForTest(downRegistrarStub{}, root)

	s := &chatServiceImpl{agentRegistry: reg}
	spec := chatLaunchSpecFixture()

	plan, err := launchplan.Build(spec, s.agentRegistry, nil)
	if err != nil {
		t.Fatalf("D1/Phase F: chat launch with a down registry hard-failed (must degrade): %v", err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("D1/Phase F: degraded chat plan failed Validate: %v", err)
	}
	// The degraded plan's provider is the composer/profile fallback —
	// exactly spec.Provider. The composer pick wins.
	if plan.Provider.ID != spec.Provider {
		t.Errorf("D1/Phase F: degraded plan provider = %q, want composer/profile fallback %q",
			plan.Provider.ID, spec.Provider)
	}

	bootOpts := &runtimeagent.Options{Mode: runtimeagent.ModeLongLived}
	s.applyLaunchSpecAsPlanToBootOpts(bootOpts, spec)
	if bootOpts.Provider != spec.Provider {
		t.Errorf("D1/Phase F: degraded bootOpts.Provider = %q, want %q", bootOpts.Provider, spec.Provider)
	}
}

// TestPhaseF_ChatLaunchWorksWithNoRegistry proves D1's offline contract
// for the chat path: a chatServiceImpl with NO registry wired (the nil
// case — tests, registry-less bootstrap) still boots a boot-profile
// session fully file/spec-default.
func TestPhaseF_ChatLaunchWorksWithNoRegistry(t *testing.T) {
	s := &chatServiceImpl{agentRegistry: nil}
	spec := chatLaunchSpecFixture()

	bootOpts := &runtimeagent.Options{Mode: runtimeagent.ModeLongLived}
	s.applyLaunchSpecAsPlanToBootOpts(bootOpts, spec)

	if bootOpts.Provider != spec.Provider {
		t.Errorf("D1/Phase F: no-registry bootOpts.Provider = %q, want %q", bootOpts.Provider, spec.Provider)
	}
	if bootOpts.BootPromptOverride != spec.BootPrompt {
		t.Errorf("D1/Phase F: no-registry BootPromptOverride = %q, want %q",
			bootOpts.BootPromptOverride, spec.BootPrompt)
	}
}

// TestPhaseF_ChatPlanGoesThroughPlanFromLaunch proves a chat session's
// assembled plan goes through agentlaunch.PlanFromLaunch to a
// Validate()-clean LaunchPlan: the PlanFromLaunch-specific provenance
// annotations and inline-boot-body shape are present on the plan the
// chat path builds.
func TestPhaseF_ChatPlanGoesThroughPlanFromLaunch(t *testing.T) {
	root := writeChatRuntimeBindingCatalog(t, "claude")
	reg := agentregistry.Build(root, "", nil)
	spec := chatLaunchSpecFixture()

	plan, err := launchplan.Build(spec, reg, nil)
	if err != nil {
		t.Fatalf("Phase F: chat Build failed: %v", err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("Phase F: chat plan failed Validate: %v", err)
	}
	// PlanFromLaunch carries the rendered boot body inline as BootContent.
	if plan.BootProfile.Inline == nil {
		t.Fatal("Phase F: plan.BootProfile.Inline is nil — not assembled via PlanFromLaunch")
	}
	if plan.BootProfile.Inline.BootContent != spec.BootPrompt {
		t.Error("Phase F: inline boot content diverged from the compiled spec — PlanFromLaunch not driven")
	}
	if plan.BootProfile.Inline.BootMode != agentlaunch.BootModePlanted {
		t.Errorf("Phase F: inline BootMode = %q, want planted (PlanFromLaunch stamp)",
			plan.BootProfile.Inline.BootMode)
	}
	// PlanFromLaunch stamps launch provenance onto Metadata.Annotations.
	if plan.Metadata.Annotations["agentlaunch.launch_spec"] == "" {
		t.Error("Phase F: plan metadata missing the PlanFromLaunch launch_spec annotation")
	}
	// §4.2 — the agent identity is resolved caller-side onto the plan.
	if plan.Agent.ID == "" {
		t.Error("Phase F: plan.Agent.ID is empty — agent identity must be caller-resolved")
	}
}

// TestPhaseF_ChatUXUnchanged_PlanPathMatchesSpecOverlay is the no-
// behavior-change guarantee. The Phase F plan path must project the SAME
// boot-opts the pre-Phase-F applyLaunchSpecToBootOpts spec-direct overlay
// produced — same Provider, Workdir, Env, BootPrompt, ExtraArgs. A
// divergence here means a chat user would observe a different boot.
func TestPhaseF_ChatUXUnchanged_PlanPathMatchesSpecOverlay(t *testing.T) {
	spec := chatLaunchSpecFixture()

	// Legacy spec-direct overlay (the pre-Phase-F behavior).
	legacy := &runtimeagent.Options{Mode: runtimeagent.ModeLongLived}
	applyLaunchSpecToBootOpts(legacy, spec)

	// Phase F plan path with a registry that has a binding for the runner.
	root := writeChatRuntimeBindingCatalog(t, "claude")
	reg := agentregistry.Build(root, "", nil)
	s := &chatServiceImpl{agentRegistry: reg}
	planned := &runtimeagent.Options{Mode: runtimeagent.ModeLongLived}
	s.applyLaunchSpecAsPlanToBootOpts(planned, spec)

	if planned.Provider != legacy.Provider {
		t.Errorf("Provider drift: plan path %q, legacy %q", planned.Provider, legacy.Provider)
	}
	if planned.Workdir != legacy.Workdir {
		t.Errorf("Workdir drift: plan path %q, legacy %q", planned.Workdir, legacy.Workdir)
	}
	if planned.BootPromptOverride != legacy.BootPromptOverride {
		t.Errorf("BootPromptOverride drift: plan path %q, legacy %q",
			planned.BootPromptOverride, legacy.BootPromptOverride)
	}
	if planned.Env["PROFILE_KEY"] != legacy.Env["PROFILE_KEY"] {
		t.Errorf("Env drift: plan path %v, legacy %v", planned.Env, legacy.Env)
	}
	if len(planned.ExtraArgs) != len(legacy.ExtraArgs) {
		t.Fatalf("ExtraArgs length drift: plan path %v, legacy %v", planned.ExtraArgs, legacy.ExtraArgs)
	}
	for i := range planned.ExtraArgs {
		if planned.ExtraArgs[i] != legacy.ExtraArgs[i] {
			t.Errorf("ExtraArgs[%d] drift: plan path %q, legacy %q", i, planned.ExtraArgs[i], legacy.ExtraArgs[i])
		}
	}
}

// TestPhaseF_ApplyLaunchPlanToBootOpts_PrecedenceAndNilSafety pins the
// projection helper's precedence rules and nil-safety, mirroring the
// applyLaunchSpecToBootOpts pinned tests so a future refactor cannot
// quietly swap the semantics.
func TestPhaseF_ApplyLaunchPlanToBootOpts_PrecedenceAndNilSafety(t *testing.T) {
	spec := chatLaunchSpecFixture()

	t.Run("nil bootOpts is a no-op (no panic)", func(t *testing.T) {
		applyLaunchPlanToBootOpts(nil, agentlaunch.LaunchPlan{}, spec)
	})

	t.Run("caller-supplied Provider wins over plan", func(t *testing.T) {
		plan, err := launchplan.Build(spec, nil, nil)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		opts := &runtimeagent.Options{Provider: "codex"}
		applyLaunchPlanToBootOpts(opts, plan, spec)
		if opts.Provider != "codex" {
			t.Errorf("Provider = %q, want codex (caller-supplied wins)", opts.Provider)
		}
	})

	t.Run("caller-supplied Workdir wins over plan", func(t *testing.T) {
		plan, err := launchplan.Build(spec, nil, nil)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		opts := &runtimeagent.Options{Workdir: "/session-set"}
		applyLaunchPlanToBootOpts(opts, plan, spec)
		if opts.Workdir != "/session-set" {
			t.Errorf("Workdir = %q, want /session-set (caller-supplied wins)", opts.Workdir)
		}
	})

	t.Run("no resume field touched", func(t *testing.T) {
		plan, err := launchplan.Build(spec, nil, nil)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		opts := &runtimeagent.Options{Mode: runtimeagent.ModeLongLived}
		applyLaunchPlanToBootOpts(opts, plan, spec)
		if opts.Mode != runtimeagent.ModeLongLived {
			t.Errorf("Mode = %v, want ModeLongLived (helper must not change Mode)", opts.Mode)
		}
		if opts.ResumeFromCheckpoint != "" {
			t.Errorf("ResumeFromCheckpoint = %q, want empty", opts.ResumeFromCheckpoint)
		}
		if opts.IsRelaunch {
			t.Error("IsRelaunch true after plan apply, want false")
		}
	})
}

// TestPhaseF_ApplyLaunchSpecAsPlan_NilSpecIsNoOp confirms the chat-side
// seam is safe to call with a nil spec — matching the surrounding
// helper pattern (the call site relies on this).
func TestPhaseF_ApplyLaunchSpecAsPlan_NilSpecIsNoOp(t *testing.T) {
	s := &chatServiceImpl{}
	opts := &runtimeagent.Options{Provider: "orig"}
	s.applyLaunchSpecAsPlanToBootOpts(opts, nil)
	if opts.Provider != "orig" {
		t.Errorf("Provider = %q, want orig (nil spec must be a no-op)", opts.Provider)
	}
	// nil opts: no panic.
	s.applyLaunchSpecAsPlanToBootOpts(nil, chatLaunchSpecFixture())
}

// TestPhaseF_ContainerThreadsSharedRegistry is a wiring guard: the
// ChatServiceConfig.AgentRegistry field must land on chatServiceImpl so
// the chat path and the standalone launcher share ONE registrar
// instance (the locked-decision requirement that there is one registry).
func TestPhaseF_ContainerThreadsSharedRegistry(t *testing.T) {
	reg := agentregistry.Build("", "", nil)
	svc := NewChatService(ChatServiceConfig{AgentRegistry: reg})
	impl, ok := svc.(*chatServiceImpl)
	if !ok {
		t.Fatalf("NewChatService returned %T, want *chatServiceImpl", svc)
	}
	if impl.agentRegistry != reg {
		t.Error("ChatServiceConfig.AgentRegistry not threaded onto chatServiceImpl — chat path would build its own registry")
	}
}
