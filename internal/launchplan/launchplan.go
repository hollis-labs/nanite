// Package launchplan is the SINGLE shared plan-assembly seam for every
// Nanite launch consumer (S5 platform-reshape, Phase F).
//
// # Why this package exists
//
// Phase C (commit 259a265) added C2's PlanFromLaunch adoption to the
// STANDALONE launcher only: internal/launcher/planbridge.go's
// buildLaunchPlan resolved a runner→RuntimeBinding registry-primary and
// assembled a shared agentlaunch.LaunchPlan via agentlaunch.PlanFromLaunch.
// The GUI chat launch path (internal/service — driveBootSession /
// applyLaunchSpecToBootOpts) never assembled a LaunchPlan at all; it
// overlaid the compiled bootprofile.LaunchSpec directly onto agent.Options.
//
// Phase F flips the chat path to the SAME seam. Rather than re-implement
// plan assembly inside internal/service (two implementations that would
// drift), the plan-assembly logic moved here, exported as Build. Both
// internal/launcher and internal/service now call this ONE function, so
// a standalone launch and a GUI chat boot-profile launch resolve their
// runtime binding registry-primary and assemble their plan identically.
//
// # What Build does (locked decisions D1 + §4.1 + §4.2)
//
//   - runtime binding resolves registry-primary through the shared
//     registrar (agentregistry.Registry.ResolveRuntimeBinding) with an
//     explicit, observable fallback to a spec/profile default when the
//     registry has no binding for the runner or is unreachable;
//   - agent identity is resolved CALLER-SIDE — it is never a LaunchSpec
//     or directory entity;
//   - the LaunchPlan is assembled by agentlaunch.PlanFromLaunch, the pure
//     shipped bridge, NOT a hand-rolled builder; PlanFromLaunch itself
//     Validate()s the assembled plan.
//
// # Why a synthetic LaunchSpec / LaunchBag / RenderResult
//
// PlanFromLaunch consumes the S4 launch model (agentlaunch.LaunchSpec +
// LaunchBag + RenderResult). Nanite's bootprofile compiler produces its
// own already-compiled bootprofile.LaunchSpec with a fully-rendered
// BootPrompt. Per the locked decision, Nanite keeps compiling
// bootprofile.LaunchSpec as the STABLE VIEW and converts it here: the
// rendered BootPrompt becomes RenderResult.Body, the lifecycle knobs
// become RenderResult.ResolvedInputs. The agentlaunch.LaunchSpec /
// LaunchBag carry provenance only (PlanFromLaunch reads them solely for
// annotations) so a minimal synthetic pair is correct and faithful.
package launchplan

import (
	"fmt"
	"log/slog"

	"github.com/hollis-labs/go-agent-launch/agentlaunch"

	"github.com/hollis-labs/nanite/internal/agentregistry"
	"github.com/hollis-labs/nanite/internal/bootprofile"
)

// RuntimeBindingForSpec is the file/spec-default RuntimeBinding derived
// from a compiled bootprofile.LaunchSpec. It is the §4.1 fallback the
// caller hands to the registry resolver — used verbatim when the
// registry has no runtime-binding for the runner or is unreachable.
//
// Both launch consumers run the long-lived NDJSON-over-stdio shape Nanite
// registers for claude (RuntimeStreamingStdio): standalone launches run
// it directly, and chat boot-profile sessions are ModeLongLived. codex/
// opencode still spawn subprocess-per-turn inside Nanite's runtime; the
// plan-level runtime kind only has to be matrix-legal, and
// streaming-stdio is.
func RuntimeBindingForSpec(spec *bootprofile.LaunchSpec) agentlaunch.RuntimeBinding {
	rb := agentlaunch.RuntimeBinding{
		RuntimeKind: agentlaunch.RuntimeStreamingStdio,
	}
	if spec != nil {
		rb.Provider = spec.Provider
		if len(spec.Args) > 0 {
			rb.Args = append([]string(nil), spec.Args...)
		}
	}
	return rb
}

// AgentSpecForLaunch resolves the launch's agent identity caller-side
// (§4.2 — the agent is a caller-resolved input to PlanFromLaunch, never
// a LaunchSpec/directory entity). It is derived from the compiled
// profile's identity block.
func AgentSpecForLaunch(spec *bootprofile.LaunchSpec) agentlaunch.AgentSpec {
	if spec == nil {
		return agentlaunch.AgentSpec{}
	}
	id := spec.Identity.LineageAlias
	if id == "" {
		id = spec.ProfileID
	}
	return agentlaunch.AgentSpec{
		ID:   id,
		Name: spec.UILabel,
	}
}

// Build assembles a validated shared agentlaunch.LaunchPlan from a
// compiled bootprofile.LaunchSpec via agentlaunch.PlanFromLaunch.
//
// reg is the shared registrar; when nil the resolution is fully
// file/spec-default (the launch still works — D1, offline-safe). The
// runtime binding resolves registry-primary through reg with the
// spec-default fallback; the resulting plan is Validate()-clean.
//
// This is the ONE implementation both internal/launcher and
// internal/service call (Phase F). The composer/profile provider
// selection stays authoritative: it is spec.Provider, which becomes the
// runner id; registry resolution only RESOLVES that runner — it never
// substitutes a different provider. When the registry has no binding or
// is down, the fallback (RuntimeBindingForSpec) — carrying exactly the
// composer/profile provider — is used verbatim. The user's UI provider
// pick always wins.
func Build(spec *bootprofile.LaunchSpec, reg *agentregistry.Registry, log *slog.Logger) (agentlaunch.LaunchPlan, error) {
	if spec == nil {
		return agentlaunch.LaunchPlan{}, fmt.Errorf("launchplan: Build: nil spec")
	}
	if log == nil {
		log = slog.Default()
	}

	// §4.1 — the runner input is authoritative for the runtime binding.
	// The runner id is the bare provider name the compiler normalized
	// onto spec.Provider (the composer/profile selection).
	runnerID := spec.Provider
	fallback := RuntimeBindingForSpec(spec)

	var (
		runtime agentlaunch.RuntimeBinding
		err     error
	)
	if reg != nil {
		// Registry-primary resolution with an explicit, observable
		// fallback (D1) — see agentregistry.Registry.ResolveRuntimeBinding.
		runtime, err = reg.ResolveRuntimeBinding(runnerID, fallback, log)
		if err != nil {
			return agentlaunch.LaunchPlan{}, fmt.Errorf("launchplan: resolve runtime binding: %w", err)
		}
	} else {
		// No registry wired — the launch is fully file/spec-default. This
		// is the offline path; the fallback is authoritative.
		if verr := fallback.Validate(); verr != nil {
			return agentlaunch.LaunchPlan{}, fmt.Errorf("launchplan: file/spec runtime binding invalid: %w", verr)
		}
		runtime = fallback
	}

	// Build the synthetic S4 launch model. The agentlaunch.LaunchSpec /
	// LaunchBag are provenance-only inputs to PlanFromLaunch; the
	// rendered boot body and lifecycle inputs are the load-bearing data.
	projectID := "nanite"
	if spec.Identity.Project != "" {
		projectID = spec.Identity.Project
	}
	alSpec := agentlaunch.LaunchSpec{ID: launchSpecCatalogID(spec)}
	alBag := agentlaunch.LaunchBag{
		Spec: alSpec.ID,
		Name: launchBagName(spec),
	}
	render := agentlaunch.RenderResult{
		Body: spec.BootPrompt,
		ResolvedInputs: map[string]any{
			"project":                      projectID,
			agentlaunch.LaunchInputWorkDir: spec.Workdir,
			// "hybrid" maps to WorkspacePersistent in PlanFromLaunch —
			// both consumers reserve a long-lived workspace dir under
			// WorkspacesRoot (agent.Boot does the actual reservation; the
			// plan is built + validated, never planted).
			agentlaunch.LaunchInputIsolation: "hybrid",
		},
	}

	plan, err := agentlaunch.PlanFromLaunch(agentlaunch.PlanFromLaunchInput{
		Spec:    alSpec,
		Bag:     alBag,
		Render:  render,
		Runtime: runtime,
		Agent:   AgentSpecForLaunch(spec),
		Mode:    agentlaunch.LaunchInteractive,
	})
	if err != nil {
		return agentlaunch.LaunchPlan{}, fmt.Errorf("launchplan: assemble launch plan: %w", err)
	}
	// PlanFromLaunch sets Project.ID from the "project" resolved input.
	// Surface MCP allow-list from the compiled spec onto the plan base.
	if len(spec.MCPServers) > 0 {
		plan.MCP = agentlaunch.MCPSpec{Allowlist: append([]string(nil), spec.MCPServers...)}
	}
	return plan, nil
}

// launchSpecCatalogID returns a stable agentlaunch.LaunchSpec id for the
// synthetic provenance spec. The compiled profile's LaunchID is the
// natural value; ProfileID is the fallback.
func launchSpecCatalogID(spec *bootprofile.LaunchSpec) string {
	if spec.LaunchID != "" {
		return spec.LaunchID
	}
	return spec.ProfileID
}

// launchBagName returns a stable LaunchBag name for provenance.
func launchBagName(spec *bootprofile.LaunchSpec) string {
	if spec.ProfileID != "" {
		return spec.ProfileID
	}
	return "nanite-launch"
}
