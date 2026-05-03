package dispatch

import (
	"github.com/hollis-labs/nanite/internal/classify"
)

// Role names a position in the three-role agent model. Surfaces are
// assigned per-role at boot and do not change mid-turn (harness spec §1).
type Role int

const (
	RoleInvalid Role = iota
	// RoleChat is the user-facing harness agent. Static, narrow surface.
	RoleChat
	// RoleWorker is a dispatched execution agent. Full task-shaped surface.
	RoleWorker
	// RolePlanner is a dispatched planning agent. Breaks down a task.
	RolePlanner
)

// String returns the canonical lower-case role name.
func (r Role) String() string {
	switch r {
	case RoleChat:
		return "chat"
	case RoleWorker:
		return "worker"
	case RolePlanner:
		return "planner"
	default:
		return "invalid"
	}
}

// IsValid reports whether r is a known role.
func (r Role) IsValid() bool { return r >= RoleChat && r <= RolePlanner }

// WorkerRoleSlug is the agent slug spawned for Worker-role dispatch.
// Matches the agent profile shipped in config/agents/worker.yaml.
const WorkerRoleSlug = "worker"

// PlannerRoleSlug is the agent slug spawned for Planner-role dispatch.
// Falls back to Worker when no dedicated planner profile is registered;
// see AssignRole. The slug is reserved for the planner profile that lands
// alongside the harness three-role model when a separate Planner identity
// is needed.
const PlannerRoleSlug = "planner"

// RoleAssignment is the dispatch decision returned by AssignRole. It
// carries the target role plus the agent slug to spawn for that role.
// The tool surface for Worker/Planner is owned by the spawned profile,
// not this package.
type RoleAssignment struct {
	Role     Role
	// AgentSlug is the slug of the agent profile to spawn. The subagent
	// runner resolves it against the agent registry.
	AgentSlug string
	// Mode is the subagent execution mode (sync / async / api / interactive).
	// AssignRole returns ModeSync for inline/subagent and a hint of "async"
	// for background; the dispatch primitive applies its own override based
	// on caller intent.
	Mode string
}

// AssignRole maps a (ScopeTier, ExecutionPattern) classification to a
// dispatch target. Open question: when a Planner agent profile is not
// yet registered, the assignment falls back to Worker so dispatch is
// still possible. Once a dedicated planner profile lands the fallback
// can be retired.
//
// Mapping rationale:
//   - PatternBackground → Worker, ModeAsync. Background work is execution,
//     not planning; the user expects a long-running task to finish.
//   - TierOpen × PatternSubagent → Planner, ModeSync. Open-scope tasks
//     warrant breakdown before execution.
//   - Otherwise → Worker, ModeSync. Default to execution.
//
// Trivial-tier work is the Chat agent's own concern; the Chat harness
// prompt instructs it to respond directly without dispatching. AssignRole
// still returns a valid Worker assignment for trivial tier so callers
// that bypass the Chat harness get a sensible default.
func AssignRole(tier classify.ScopeTier, pattern classify.ExecutionPattern) RoleAssignment {
	switch pattern {
	case classify.PatternBackground:
		return RoleAssignment{
			Role:      RoleWorker,
			AgentSlug: WorkerRoleSlug,
			Mode:      "async",
		}
	}
	if tier == classify.TierOpen && pattern == classify.PatternSubagent {
		return RoleAssignment{
			Role:      RolePlanner,
			AgentSlug: PlannerRoleSlug,
			Mode:      "sync",
		}
	}
	return RoleAssignment{
		Role:      RoleWorker,
		AgentSlug: WorkerRoleSlug,
		Mode:      "sync",
	}
}
