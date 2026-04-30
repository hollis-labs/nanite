package dispatch

import (
	"strings"

	"github.com/hollis-labs/go-providers/provider"
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

// ChatHarnessTemplateSlug is the prompt-template slug seeded by migration
// 027 that marks an agent as wearing the Chat-role harness identity.
// Agents bound to this template have their tool surface clamped to
// ChatToolSurface.
const ChatHarnessTemplateSlug = "chat-role-harness"

// ChatHarnessTemplateID is the deterministic ID for the seeded harness
// prompt template (migration 027). Detection uses the slug as primary
// signal; the ID is documented here for cross-reference.
const ChatHarnessTemplateID = "blt-chat-harness-001"

// WorkerRoleSlug is the agent slug spawned for Worker-role dispatch.
// Matches the agent profile shipped in config/agents/worker.yaml.
const WorkerRoleSlug = "worker"

// PlannerRoleSlug is the agent slug spawned for Planner-role dispatch.
// Falls back to Worker when no dedicated planner profile is registered;
// see AssignRole. The slug is reserved for the planner profile that lands
// alongside the harness three-role model when a separate Planner identity
// is needed.
const PlannerRoleSlug = "planner"

// ChatToolSurface is the fixed allow-list of tool-name prefixes the Chat
// agent may invoke. Worker/Planner surfaces are NOT pinned here — they
// are governed by the spawned agent profile's own permissions, exactly
// as before. Only the harness side is enforced.
//
// The list is intentionally small: todos, plans, scratchpad, peer_query
// (messaging), narration (envelope renderers), plus the executeTask
// primitive itself. Meta-tools (cache fetch/search, request_tools) are
// always allowed regardless of surface — the LLM needs them to recover
// truncated tool results.
//
// Each entry is a prefix. A tool name passes when it equals or starts
// with any entry. This keeps the surface stable as the underlying
// nanite_todo_*, nanite_plan_*, nanite_scratchpad_*, nanite_message_*,
// nanite_show_*, and nanite_execute_task tools evolve.
//
// Post MCP internalization (CW-20260427-0017, ADR-002) the surface
// guarantee is "exact prefix match against this list". MCP-origin tools
// no longer carry a `mcp__server__` prefix to reject by name — they are
// kept off the Chat surface because they don't share any prefix in this
// list (e.g. an MCP-published `task_create` or `memory_write` simply
// fails the `nanite_*` / meta-tool checks below and is filtered out).
var ChatToolSurface = []string{
	// Todos primitive.
	"nanite_todo_",
	// Plans primitive.
	"nanite_plan_",
	// Scratchpad primitive (per-turn key/value buffer).
	"nanite_scratchpad_",
	// Peer query / messaging primitive.
	"nanite_message_",
	"nanite_handoff_",
	// Narration primitive (envelope renderers).
	"nanite_show_",
	// The dispatch primitive itself.
	"nanite_execute_task",
	// Conversation search (P8B, CW-20260420-0026).
	"nanite_chat_search",
	// Discovery primitive (A1, CW-20260429-0005). Returns a tool's
	// schema + golden examples. Reactive recovery surface — the agent
	// reaches for it when a tool's contract is unfamiliar or after a
	// schema-validation failure. The describe-required preemptive gate
	// at callShowCard (CW-20260429-0025/0027) was removed in Phase A of
	// the architectural rebalancing per docs/architecture/agent-context-architecture.md.
	"nanite_tool_describe",
	// Cheap discovery primitive (SP6, CW-20260430-0006). Returns
	// {name, summary} for every self-tool with an optional substring
	// filter. Sibling to nanite_tool_describe but ~2 orders of
	// magnitude cheaper — agents browse the surface here, then call
	// describe for the deep dive on a single tool. Reactive only — no
	// "must call before X" gate; the agent reaches for this when
	// unsure which tool exists, not as a precondition for action.
	"nanite_tool_list",
	// Pre-flight schema validation (B1, CW-20260429-0006). Read-only,
	// idempotent — the agent uses it to check args before firing a
	// high-blast-radius tool. Not a write or a panel signal, so safe to
	// keep on the Chat surface.
	"nanite_validate",
	// Learning capture (D1, CW-20260429-0009). Persists a one-sentence
	// lesson to durable memory. Idempotent on (scope, subject, hint),
	// no panel signal, no surface mutation — safe on the Chat surface
	// and load-bearing for the self-healing loop (Layer 4 of the lens).
	"nanite_remember",
	// Memory recall (SP3, CW-20260430-0003). Layer 4 read-side complement
	// to nanite_remember — the Chat agent retrieves prior lessons /
	// captured context relevant to the current turn. Reactive use only:
	// after a tool failure or before retrying an unfamiliar contract, not
	// preemptively on every turn. No "always recall before X" gate (would
	// re-introduce the c114 describe-gate anti-pattern).
	"nanite_memory_recall",
	// Panel control (J8 v1, CW-20260426-0006). The Chat agent can open and
	// close known UI drawers as a visibility-only signal — these tools never
	// mutate workspace data and are gated for plugin-shipped panels via H1
	// trust resolution at handler time.
	"nanite_panel_open",
	"nanite_panel_close",
	"nanite_signal_mode",
	// Reminders + pins (J11/D1, CW-20260426-0009 / CW-20260428-0014; surface
	// gap closed by SP2, CW-20260430-0002). Always-meant-to-be-on Chat-loop
	// primitives: deterministic time/turn-count reminders, and pin/unpin of
	// context that should ride across turns or sessions. Without these on
	// the Chat surface the agent can describe them but EnforceChatSurface
	// strips them at call time, which c120 surfaced as a misattributed
	// "MCP parser bug".
	"nanite_set_reminder",
	"nanite_pin",
	"nanite_unpin",
}

// chatSurfaceMetaToolExceptions are always allowed on the Chat surface:
// meta-tools that the loop relies on for cache recovery and progressive
// tool discovery. Listed by exact name (not prefix).
var chatSurfaceMetaToolExceptions = map[string]bool{
	"fetch_tool_result":  true,
	"search_tool_result": true,
	"request_tools":      true,
}

// IsChatSurfaceTool reports whether a tool name is permitted on the
// Chat agent's static surface.
func IsChatSurfaceTool(name string) bool {
	if chatSurfaceMetaToolExceptions[name] {
		return true
	}
	for _, prefix := range ChatToolSurface {
		if name == prefix || strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// EnforceChatSurface filters tools to the Chat agent's static surface.
// Intended for boot-time tool resolution: the Chat harness assigns a
// fixed surface that does not change mid-turn (harness spec §1).
//
// Worker/Planner surfaces are out of scope here — they are governed by
// the spawned profile's own permissions. This filter applies only when
// IsChatRoleAgent reports true for the agent in question.
func EnforceChatSurface(tools []provider.ToolDefinition) []provider.ToolDefinition {
	if len(tools) == 0 {
		return tools
	}
	out := make([]provider.ToolDefinition, 0, len(tools))
	for _, t := range tools {
		if IsChatSurfaceTool(t.Name) {
			out = append(out, t)
		}
	}
	return out
}

// PromptTemplateLister is the narrow store surface used to detect the
// Chat-role harness binding without coupling this package to the full
// store. *store.Store satisfies it; tests can implement a stub.
type PromptTemplateLister interface {
	// ListPromptTemplatesForAgent returns templates assigned to agentID.
	// The dispatch package only reads the slug field of each entry, but
	// returning the concrete store row keeps callers — production and
	// tests — from synthesizing a parallel type.
	ListPromptTemplatesForAgent(agentID string) ([]PromptTemplateRef, error)
}

// PromptTemplateRef is the minimal projection IsChatRoleAgent reads from
// a prompt-template row. Mirrors store.PromptTemplate's exposed fields
// so the production conversion is a struct copy at the call site.
type PromptTemplateRef struct {
	ID   string
	Slug string
}

// IsChatRoleAgent reports whether the agent identified by agentID has
// the Chat-role harness prompt template assigned (slug
// "chat-role-harness", seeded by migration 027). When the lister is nil
// or returns an error, IsChatRoleAgent returns false so callers fall
// back to the un-enforced path rather than blocking on a transient DB
// failure. The error is returned for caller-side telemetry; the bool
// is the load-bearing signal.
func IsChatRoleAgent(lister PromptTemplateLister, agentID string) (bool, error) {
	if lister == nil || agentID == "" {
		return false, nil
	}
	tpls, err := lister.ListPromptTemplatesForAgent(agentID)
	if err != nil {
		return false, err
	}
	for _, t := range tpls {
		if t.Slug == ChatHarnessTemplateSlug || t.ID == ChatHarnessTemplateID {
			return true, nil
		}
	}
	return false, nil
}

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
