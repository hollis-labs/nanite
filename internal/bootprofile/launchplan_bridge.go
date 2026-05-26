package bootprofile

import (
	"github.com/hollis-labs/agentkit/agentlaunch"
)

// launchplan_bridge.go converts a compiled Nanite LaunchSpec into the
// shared go-agent-launch declarative types (CW-20260515-0024 Phase 6
// port).
//
// # Why a bridge, not a replacement
//
// The shared agentlaunch package models a launch as a declarative
// LaunchPlan that flows through launcher.Compile → launcher.Prepare →
// providerplant.Plant. Nanite's bootprofile package produces a
// LaunchSpec — a flat, already-compiled, JSON-serializable struct that
// the Nanite chat runtime, the provider dropdown, and the Registry
// consume directly. Those consumers (and their tests) pin the
// LaunchSpec shape, so LaunchSpec stays the canonical Nanite output.
//
// This bridge exposes the LaunchSpec in the shared vocabulary so the
// downstream Phase-6 workstreams can converge on agentlaunch types
// without Nanite re-deriving them:
//
//   - CW-0025 (provider bootdir) consumes agentlaunch.BootProfileInline
//     + agentlaunch.ProviderSpec to drive the per-provider bootdir
//     layout. ToBootProfileInline / ToProviderSpec below are the inputs.
//   - CW-0027 (standalone launcher) consumes a full agentlaunch.LaunchPlan;
//     ToLaunchPlan assembles one from a compiled LaunchSpec.
//
// The bridge is one-directional (LaunchSpec → agentlaunch.*). Nanite
// keeps owning catalog loading, slot compilation, and the dropdown
// encoding; the shared types are the handoff format, not a new source
// of truth.

// ToBootProfileInline projects a compiled LaunchSpec onto the shared
// agentlaunch.BootProfileInline. The rendered BootPrompt becomes the
// inline boot prompt; the boot mode is mapped from Nanite's free-form
// BootMode hint onto the shared three-token enum.
//
// A LaunchSpec with unresolved Requirements has an empty BootPrompt
// (see compiler.go) — the resulting BootProfileInline carries an empty
// BootPrompt in that case, which is the honest representation: the
// launch is not yet bootable.
func (s *LaunchSpec) ToBootProfileInline() agentlaunch.BootProfileInline {
	if s == nil {
		return agentlaunch.BootProfileInline{}
	}
	return agentlaunch.BootProfileInline{
		BootPrompt: s.BootPrompt,
		BootMode:   sharedBootMode(s.BootMode),
	}
}

// ToProviderSpec projects a compiled LaunchSpec onto the shared
// agentlaunch.ProviderSpec. The normalized bare provider name
// (LaunchSpec.Provider — e.g. "claude", "codex") becomes ProviderSpec.ID;
// the pass-through Env and Args map onto ProviderSpec.Env / Flags.
//
// A prompt-only profile compiled with launch==nil has an empty
// Provider; the returned ProviderSpec.ID is then empty and the spec is
// NOT valid for a LaunchPlan (LaunchPlan.Validate rejects it). Callers
// that build a LaunchPlan from a prompt-only profile must filter it
// first — the same rule LaunchSpec already documents for the dropdown.
func (s *LaunchSpec) ToProviderSpec() agentlaunch.ProviderSpec {
	if s == nil {
		return agentlaunch.ProviderSpec{}
	}
	var flags []string
	if len(s.Args) > 0 {
		flags = append(flags, s.Args...)
	}
	var env map[string]string
	if len(s.Env) > 0 {
		env = make(map[string]string, len(s.Env))
		for k, v := range s.Env {
			env[k] = v
		}
	}
	return agentlaunch.ProviderSpec{
		ID:    s.Provider,
		Flags: flags,
		Env:   env,
	}
}

// ToMCPSpec projects a compiled LaunchSpec's per-profile MCP server
// allow-list onto the shared agentlaunch.MCPSpec. Nanite's MCPServers
// is an allow-list of server names, which maps to MCPSpec.Allowlist.
func (s *LaunchSpec) ToMCPSpec() agentlaunch.MCPSpec {
	if s == nil || len(s.MCPServers) == 0 {
		return agentlaunch.MCPSpec{}
	}
	allow := make([]string, len(s.MCPServers))
	copy(allow, s.MCPServers)
	return agentlaunch.MCPSpec{Allowlist: allow}
}

// LaunchPlanOptions carries the fields a shared agentlaunch.LaunchPlan
// requires that a Nanite LaunchSpec does not itself express. Nanite's
// boot-profile catalog is prompt-composition-focused; the launch
// lifecycle knobs (runtime kind, workspace mode, launch mode, project
// id) are supplied by the caller assembling the plan.
//
// CW-0027's standalone launcher owns the defaults for these — the
// bridge does not invent them, it requires the caller to be explicit
// so a plan built here always validates for a known reason.
type LaunchPlanOptions struct {
	// ProjectID is the agentlaunch.ProjectSpec.ID. Required by
	// LaunchPlan.Validate. Nanite typically passes the project slug
	// ("nanite").
	ProjectID string

	// ProjectRoot is the optional absolute project root. Empty lets
	// the shared compiler resolve a default.
	ProjectRoot string

	// AgentID is the agentlaunch.AgentSpec.ID. Required by
	// LaunchPlan.Validate. Nanite passes the agent slug.
	AgentID string

	// AgentName is the optional display name for the agent.
	AgentName string

	// Runtime is the runtime lifecycle shape. Required and validated
	// by LaunchPlan.Validate.
	Runtime agentlaunch.RuntimeKind

	// WorkspaceMode is the workspace reservation policy. Required and
	// validated by LaunchPlan.Validate.
	WorkspaceMode agentlaunch.WorkspaceMode

	// Mode is the launch lifecycle stance. Required and validated by
	// LaunchPlan.Validate.
	Mode agentlaunch.LaunchMode
}

// ToLaunchPlan assembles a shared agentlaunch.LaunchPlan from a compiled
// LaunchSpec plus the caller-supplied lifecycle options. The boot
// profile is carried inline (BootProfileRef.Inline) because Nanite has
// already compiled the prompt — there is no need to round-trip back
// through a catalog path.
//
// RETIRED FROM THE PRODUCTION PATH (S5 platform-reshape, Phase C). The
// standalone launcher no longer hand-assembles a LaunchPlan here: it now
// drives the shipped agentlaunch.PlanFromLaunch bridge (see
// internal/launcher/planbridge.go), which resolves the runtime binding
// registry-primary and Validate()s the assembled plan itself. ToLaunchPlan
// is retained ONLY so its pinned tests (launchplan_bridge_test.go) stay
// green — bootprofile.LaunchSpec remains the stable Nanite view. Do NOT
// reach for ToLaunchPlan in new code; use launcher.buildLaunchPlan.
//
// The returned plan is NOT pre-validated; callers should run
// plan.Validate() and handle the sentinel errors.
func (s *LaunchSpec) ToLaunchPlan(opts LaunchPlanOptions) agentlaunch.LaunchPlan {
	inline := s.ToBootProfileInline()
	return agentlaunch.LaunchPlan{
		Project: agentlaunch.ProjectSpec{
			ID:   opts.ProjectID,
			Root: opts.ProjectRoot,
		},
		Agent: agentlaunch.AgentSpec{
			ID:   opts.AgentID,
			Name: opts.AgentName,
		},
		Provider: s.ToProviderSpec(),
		Runtime:  opts.Runtime,
		Workspace: agentlaunch.WorkspaceSpec{
			Mode:    opts.WorkspaceMode,
			Workdir: launchSpecWorkdir(s),
		},
		BootProfile: agentlaunch.BootProfileRef{
			Inline: &inline,
		},
		MCP:  s.ToMCPSpec(),
		Mode: opts.Mode,
	}
}

// launchSpecWorkdir returns the spec's workdir, nil-safe.
func launchSpecWorkdir(s *LaunchSpec) string {
	if s == nil {
		return ""
	}
	return s.Workdir
}

// sharedBootMode maps Nanite's free-form boot-mode hint (file / stdin /
// inline, or empty for "adapter default") onto the shared three-token
// agentlaunch boot-mode enum (none / stdin / planted).
//
// Mapping rationale:
//
//   - "stdin"            → BootModeStdin   (direct equivalent).
//   - "file" / "inline"  → BootModePlanted (Nanite's file/inline modes
//     both mean "the boot body is materialized as a file the provider
//     reads", which is exactly the shared "planted" semantics).
//   - "" (adapter default) → BootModeNone  (no explicit boot file; the
//     shared enum's "none" is the closest "let the adapter decide"
//     token, and an inline profile with BootModeNone is still valid
//     per validBootMode).
//
// Any unrecognized token falls through to BootModeNone so a future
// Nanite boot-mode addition degrades safely rather than producing an
// invalid inline profile.
func sharedBootMode(nanite string) string {
	switch nanite {
	case "stdin":
		return agentlaunch.BootModeStdin
	case "file", "inline":
		return agentlaunch.BootModePlanted
	default:
		return agentlaunch.BootModeNone
	}
}
