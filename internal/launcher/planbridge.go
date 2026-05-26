package launcher

import (
	"log/slog"

	"github.com/hollis-labs/agentkit/agentlaunch"

	"github.com/hollis-labs/nanite/internal/agentregistry"
	"github.com/hollis-labs/nanite/internal/bootprofile"
	"github.com/hollis-labs/nanite/internal/launchplan"
)

// planbridge.go — the standalone launcher's handle onto the SHARED
// plan-assembly seam (S5 platform-reshape).
//
// Phase C added C2's PlanFromLaunch adoption HERE (a launcher-local
// buildLaunchPlan). Phase F flips the GUI chat launch path onto the same
// seam — and rather than maintain two implementations that would drift,
// the plan-assembly logic moved to internal/launchplan.Build, the ONE
// shared implementation both internal/launcher and internal/service call.
//
// buildLaunchPlan stays as the launcher's internal name so launcher.go
// (and its pinned tests) are untouched; it is now a thin delegate.
//
// See internal/launchplan/launchplan.go for the full rationale (registry-
// primary resolution with an observable file/spec fallback — D1 + §4.1 —
// caller-side agent identity — §4.2 — and the synthetic S4 launch model
// PlanFromLaunch consumes).
func buildLaunchPlan(spec *bootprofile.LaunchSpec, reg *agentregistry.Registry, log *slog.Logger) (agentlaunch.LaunchPlan, error) {
	return launchplan.Build(spec, reg, log)
}
