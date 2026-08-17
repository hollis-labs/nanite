// Package promptrouter is the M3 deterministic phrase-match dispatch
// router: it maps substrings of the user's raw prompt text to an agent
// pattern/role/profile selection, upstream of dispatch.AssignRole. Pure
// substring matching against the Reflex catalog defined here — no LLM
// calls, no session-state evaluation.
//
// Previously named internal/reflex, renamed to remove any naming
// collision with internal/agent/reflexes — an unrelated package (the
// FU-30 predicate/event/interval steering engine for durable agents,
// which evaluates session-state signals like token usage and tool calls
// and stages actions such as inject_reminder/halt_session). Different
// system, different code, different lifecycle; the two no longer share a
// root word at all, which was the point of this rename.
//
// A Reflex is consumed by the playbook runtime (CW-20260419-0027) which
// is not yet implemented. This package exposes the content layer —
// the Reflex struct, the BuiltinReflexes set — plus the matcher
// (matcher.go), dispatcher integration (dispatcher.go), and user-override
// loader (loader.go).
//
// Integration path: when the playbook runtime lands it calls
// BuiltinReflexes() at boot, runs its phrase-match loop, and forwards the
// winning reflex's ScopeTier/ExecutionPattern hints to dispatch.AssignRole.
//
// See docs/promptrouter-catalog.md for the full schema documentation and
// integration plan.
package promptrouter

import "github.com/hollis-labs/nanite/internal/classify"

// Triggers defines when a reflex fires.
type Triggers struct {
	// UserPhraseAnyOf is a list of lower-case phrase substrings.
	// A match fires when the (normalized) user input contains ANY entry.
	UserPhraseAnyOf []string

	// ScopeTierHint, when non-zero, restricts this reflex to inputs where
	// the M1 classifier produced this tier or broader. Zero means any tier.
	ScopeTierHint classify.ScopeTier

	// ExecutionPatternHint, when non-zero, restricts this reflex to inputs
	// where the M1 classifier produced this exact pattern. Zero means any
	// pattern.
	ExecutionPatternHint classify.ExecutionPattern
}

// Resolution is the agent behavior a matched reflex resolves to.
type Resolution struct {
	// Pattern is the pattern slug from docs/agent-pattern-catalog.md.
	// One of: chat, strategist, planner, researcher, documentor, worker, reviewer.
	Pattern string

	// Role is the dispatch.Role string from internal/dispatch/role.go.
	// One of: chat, worker, planner.
	Role string

	// Profile is the agent profile slug to spawn. Overrides the
	// dispatch.AssignRole default (WorkerRoleSlug or PlannerRoleSlug).
	// Empty means use AssignRole's default.
	Profile string

	// WorkflowName, when non-empty, routes a match to a named, registered
	// workflow run (design doc "Integration with the rest of Nanite" —
	// CW-20260813-0014) instead of an ordinary Worker/Planner dispatch.
	// The MCP layer (internal/mcp/self_tools_dispatch.go) forwards it
	// verbatim to dispatch.ReflexHints.WorkflowName, which is the only way
	// dispatch.ExecuteTask bypasses AssignRole's (tier, pattern) mapping
	// (see internal/dispatch/execute.go). When set, Pattern/Role/Profile
	// above are ignored — ExecuteTask forces Role=RoleWorkflow regardless
	// of what they say. Empty (the default for every existing reflex)
	// means this reflex resolves to a Worker/Planner dispatch as before.
	WorkflowName string
}

// SideEffects are non-dispatch signals emitted when a reflex matches.
type SideEffects struct {
	// ModeSignal is emitted on the session mode bus. Consumers (UI,
	// workflow layer) observe this to open drawers or change display mode.
	// Defined v1 values: planning, research, review, document, execute.
	ModeSignal string

	// DispatchVia hints to the dispatch primitive how to run the agent.
	// Values: executeTask (sync subagent), executeBackground (async).
	// Empty means let AssignRole + caller decide.
	DispatchVia string
}

// Reflex is a single deterministic mapping from user-input pattern to
// agent behavior. The playbook runtime (CW-20260419-0027) evaluates the
// reflex set in descending Priority order and returns the first match.
type Reflex struct {
	// ID is a unique slug. Convention: <pattern>-<keyword>.
	ID string

	// Triggers defines the matching conditions.
	Triggers Triggers

	// ResolvesTo is the agent behavior on match.
	ResolvesTo Resolution

	// SideEffects are additional signals emitted on match.
	SideEffects SideEffects

	// Priority is the tiebreak when multiple reflexes match the same input.
	// Higher wins. Default 0; range 0–100.
	Priority int
}

// BuiltinReflexes returns the v1 built-in reflex set ordered by descending
// priority. The playbook runtime loads this set at boot; user overrides
// (from ~/.nanite/reflexes/) are merged after with user entries taking
// precedence for equal or higher priority.
//
// Schema documentation: docs/promptrouter-catalog.md
// Pattern catalog:      docs/agent-pattern-catalog.md
// Role constants:       internal/dispatch/role.go (RoleWorker, RolePlanner)
// ScopeTier constants:  internal/classify/scope.go
func BuiltinReflexes() []Reflex {
	return []Reflex{
		{
			ID: "background-long-task",
			Triggers: Triggers{
				UserPhraseAnyOf: []string{
					"in the background",
					"async",
					"when you get a chance",
					"overnight",
					"index the whole",
					"crawl the entire",
				},
				ExecutionPatternHint: classify.PatternBackground,
			},
			ResolvesTo:  Resolution{Pattern: "worker", Role: "worker", Profile: "worker"},
			SideEffects: SideEffects{DispatchVia: "executeBackground"},
			Priority:    25,
		},
		{
			ID: "planner-mention",
			Triggers: Triggers{
				UserPhraseAnyOf: []string{
					"let's plan",
					"let's work on",
					"sprint",
					"plan this",
					"plan out",
					"create a plan",
				},
				ScopeTierHint: classify.TierOpen,
			},
			ResolvesTo:  Resolution{Pattern: "planner", Role: "planner", Profile: "planner"},
			SideEffects: SideEffects{ModeSignal: "planning", DispatchVia: "executeTask"},
			Priority:    20,
		},
		{
			ID: "planner-large-task",
			Triggers: Triggers{
				UserPhraseAnyOf: []string{
					"big project",
					"multi-step",
					"break this down",
					"sequence of",
					"phases",
					"end to end",
				},
				ScopeTierHint: classify.TierLarge,
			},
			ResolvesTo:  Resolution{Pattern: "planner", Role: "planner", Profile: "planner"},
			SideEffects: SideEffects{ModeSignal: "planning", DispatchVia: "executeTask"},
			Priority:    18,
		},
		{
			ID: "researcher-mention",
			Triggers: Triggers{
				UserPhraseAnyOf: []string{
					"research",
					"investigate",
					"look into",
					"find out",
					"dig into",
					"what does",
					"find all",
					"summarize the state",
				},
			},
			ResolvesTo:  Resolution{Pattern: "researcher", Role: "worker", Profile: "researcher"},
			SideEffects: SideEffects{ModeSignal: "research", DispatchVia: "executeTask"},
			Priority:    15,
		},
		{
			ID: "reviewer-mention",
			Triggers: Triggers{
				UserPhraseAnyOf: []string{
					"review",
					"assess",
					"second opinion",
					"critique",
					"audit",
					"check this",
					"give me feedback on",
				},
			},
			ResolvesTo:  Resolution{Pattern: "reviewer", Role: "worker", Profile: "reviewer"},
			SideEffects: SideEffects{ModeSignal: "review"},
			Priority:    15,
		},
		{
			ID: "documentor-mention",
			Triggers: Triggers{
				UserPhraseAnyOf: []string{
					"document",
					"write up",
					"summarize",
					"capture",
					"write a doc",
					"add to the kb",
					"create an adr",
					"write the changelog",
				},
			},
			// Profile left empty — still no `documentor` agent profile as of
			// the CW-20260815-0002 re-audit (internal/agent/builtin/profiles/
			// has no documentor.md, and no workspace-local .nanite/agents/
			// file registers a matching slug). Falls back to AssignRole's
			// default (`worker`). Set Profile: "documentor" once one ships.
			ResolvesTo:  Resolution{Pattern: "documentor", Role: "worker"},
			SideEffects: SideEffects{ModeSignal: "document", DispatchVia: "executeTask"},
			Priority:    12,
		},
		{
			ID: "strategist-mention",
			Triggers: Triggers{
				UserPhraseAnyOf: []string{
					"brainstorm",
					"explore",
					"what about",
					"what if",
					"think through",
					"options for",
					"tradeoffs",
					"pros and cons",
				},
				ScopeTierHint: classify.TierMedium,
			},
			// Profile left empty — still no `strategist` agent profile as of
			// the CW-20260815-0002 re-audit. `.nanite/agents/content-strategist.md`
			// exists but is a different, narrower role (Glyph editorial/content
			// operations, not the general brainstorm/tradeoffs pattern this
			// reflex targets) — not a match. Falls back to AssignRole's
			// default (`worker`). Set Profile: "strategist" once a matching
			// profile ships.
			ResolvesTo:  Resolution{Pattern: "strategist", Role: "worker"},
			SideEffects: SideEffects{ModeSignal: "planning"},
			Priority:    10,
		},
		{
			ID: "worker-execute",
			Triggers: Triggers{
				UserPhraseAnyOf: []string{
					"build",
					"implement",
					"fix",
					"refactor",
					"write the code",
					"add the feature",
					"make it",
					"run the migration",
				},
			},
			ResolvesTo:  Resolution{Pattern: "worker", Role: "worker", Profile: "worker"},
			SideEffects: SideEffects{ModeSignal: "execute", DispatchVia: "executeTask"},
			Priority:    10,
		},
	}
}
