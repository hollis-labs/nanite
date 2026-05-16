package bootprofile

import (
	"errors"
	"testing"

	"github.com/hollis-labs/go-agent-launch/agentlaunch"
)

// TestToBootProfileInline_CarriesPromptAndMode pins that a compiled
// LaunchSpec projects onto agentlaunch.BootProfileInline with the
// rendered prompt and a mapped boot mode.
func TestToBootProfileInline_CarriesPromptAndMode(t *testing.T) {
	prof := newTestProfile()
	launch := &Launch{ID: "l", Provider: "pty-claude", BootMode: "file"}
	spec, err := Compile(prof, launch, nil, "")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	inline := spec.ToBootProfileInline()
	if inline.BootPrompt == "" {
		t.Fatal("BootProfileInline.BootPrompt empty; expected rendered prompt")
	}
	if inline.BootMode != agentlaunch.BootModePlanted {
		t.Fatalf("BootMode = %q, want %q (file → planted)", inline.BootMode, agentlaunch.BootModePlanted)
	}
}

func TestSharedBootMode_Mapping(t *testing.T) {
	cases := map[string]string{
		"stdin":  agentlaunch.BootModeStdin,
		"file":   agentlaunch.BootModePlanted,
		"inline": agentlaunch.BootModePlanted,
		"":       agentlaunch.BootModeNone,
		"bogus":  agentlaunch.BootModeNone,
	}
	for in, want := range cases {
		if got := sharedBootMode(in); got != want {
			t.Errorf("sharedBootMode(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestToProviderSpec_NormalizedID confirms the bare normalized provider
// name lands on ProviderSpec.ID and Env/Args carry through.
func TestToProviderSpec_NormalizedID(t *testing.T) {
	prof := newTestProfile()
	launch := &Launch{
		ID:       "l",
		Provider: "pty-claude",
		Env:      map[string]string{"K": "V"},
		Args:     []string{"--flag"},
	}
	spec, err := Compile(prof, launch, nil, "")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	ps := spec.ToProviderSpec()
	if ps.ID != "claude" {
		t.Fatalf("ProviderSpec.ID = %q, want normalized %q", ps.ID, "claude")
	}
	if ps.Env["K"] != "V" {
		t.Fatalf("ProviderSpec.Env lost K: %v", ps.Env)
	}
	if len(ps.Flags) != 1 || ps.Flags[0] != "--flag" {
		t.Fatalf("ProviderSpec.Flags = %v", ps.Flags)
	}
}

func TestToMCPSpec_Allowlist(t *testing.T) {
	prof := newTestProfile() // MCPServers: ["vanta"]
	spec, err := Compile(prof, &Launch{ID: "l", Provider: "anthropic"}, nil, "")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	mcp := spec.ToMCPSpec()
	if len(mcp.Allowlist) != 1 || mcp.Allowlist[0] != "vanta" {
		t.Fatalf("MCPSpec.Allowlist = %v", mcp.Allowlist)
	}
}

// TestToLaunchPlan_Validates confirms a plan assembled from a compiled
// LaunchSpec plus lifecycle options passes agentlaunch.LaunchPlan.Validate.
func TestToLaunchPlan_Validates(t *testing.T) {
	prof := newTestProfile()
	launch := &Launch{ID: "l", Provider: "pty-claude", Workdir: "/work", BootMode: "file"}
	spec, err := Compile(prof, launch, nil, "")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan := spec.ToLaunchPlan(LaunchPlanOptions{
		ProjectID:     "nanite",
		AgentID:       "backend",
		Runtime:       agentlaunch.RuntimePTY,
		WorkspaceMode: agentlaunch.WorkspaceShared,
		Mode:          agentlaunch.LaunchInteractive,
	})
	if err := plan.Validate(); err != nil {
		t.Fatalf("LaunchPlan.Validate: %v", err)
	}
	if plan.Provider.ID != "claude" {
		t.Fatalf("plan.Provider.ID = %q", plan.Provider.ID)
	}
	if plan.Workspace.Workdir != "/work" {
		t.Fatalf("plan.Workspace.Workdir = %q", plan.Workspace.Workdir)
	}
	if plan.BootProfile.Inline == nil {
		t.Fatal("plan.BootProfile.Inline is nil")
	}
}

// TestToLaunchPlan_PromptOnlyFailsValidate documents that a prompt-only
// profile (compiled with launch==nil → empty Provider) yields a plan
// that LaunchPlan.Validate rejects with ErrMissingProviderID — the same
// "not bootable" rule LaunchSpec already pins for the dropdown.
func TestToLaunchPlan_PromptOnlyFailsValidate(t *testing.T) {
	prof := newTestProfile()
	spec, err := Compile(prof, nil, nil, "")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan := spec.ToLaunchPlan(LaunchPlanOptions{
		ProjectID:     "nanite",
		AgentID:       "backend",
		Runtime:       agentlaunch.RuntimePTY,
		WorkspaceMode: agentlaunch.WorkspaceShared,
		Mode:          agentlaunch.LaunchInteractive,
	})
	if err := plan.Validate(); !errors.Is(err, agentlaunch.ErrMissingProviderID) {
		t.Fatalf("Validate err = %v, want ErrMissingProviderID", err)
	}
}

// TestToLaunchPlan_NilSafe confirms the bridge methods tolerate a nil
// receiver.
func TestToLaunchPlan_NilSafe(t *testing.T) {
	var spec *LaunchSpec
	if got := spec.ToBootProfileInline(); got != (agentlaunch.BootProfileInline{}) {
		t.Fatalf("nil ToBootProfileInline = %+v", got)
	}
	if got := spec.ToProviderSpec(); got.ID != "" {
		t.Fatalf("nil ToProviderSpec = %+v", got)
	}
	if got := spec.ToMCPSpec(); len(got.Allowlist) != 0 {
		t.Fatalf("nil ToMCPSpec = %+v", got)
	}
}
