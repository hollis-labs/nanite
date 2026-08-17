package reflex

import (
	"sort"

	"github.com/hollis-labs/nanite/internal/classify"
	"github.com/hollis-labs/nanite/internal/dispatch"
)

// MatchLogger is the narrow store interface the dispatcher uses to persist a
// reflex match event. *store.Store satisfies it when the reflex_log.go
// methods are wired. Tests stub it with a no-op or capturing implementation.
type MatchLogger interface {
	// LogReflexMatch persists one match row to playbook_match_log.
	LogReflexMatch(entry ReflexMatchEntry) error
}

// ReflexMatchEntry is the input shape for LogReflexMatch.
type ReflexMatchEntry struct {
	SessionID           string
	TurnID              string // optional; empty stored as NULL
	ReflexID            string
	Priority            int
	Source              string // always "reflex" in this package
	MatchedInputExcerpt string // first 200 chars of raw input
	HintTier            string // ScopeTier.String()
	HintPattern         string // ExecutionPattern.String()
	ProfileSlug         string
	Mode                string // ModeSignal from SideEffects

	// RawInputText and SentInputText (CW-20260816-0068) carry the full,
	// untruncated raw-vs-dispatched text pair for the turn's audit trail.
	// RawInputText is the user's raw input as typed; SentInputText is the
	// text actually sent/dispatched to the spawned agent after any
	// pre-dispatch rewrite (e.g. E2 grounding's memory-block prepend).
	// Callers that perform no rewrite should set both to the same value —
	// the writer (store.LogReflexMatch) collapses identical pairs to empty
	// strings before persisting, so equal values never bloat the table.
	RawInputText  string
	SentInputText string
}

// AssignRoleWithReflex is the integration entry point for the playbook
// runtime. It:
//
//  1. Runs the reflex matcher over the full reflex set (builtins + user overrides).
//  2. On a match: overrides the RoleAssignment's Role, AgentSlug, and Mode with
//     the matched reflex's hints; logs the match via logger (if non-nil).
//  3. On a miss: falls through to dispatch.AssignRole(m1Tier, m1Pattern) unchanged.
//
// sessionID and turnID are used for match logging. turnID may be empty.
// logger may be nil (match is evaluated but not persisted).
//
// This is the authoritative integration seam: callers that previously called
// dispatch.AssignRole directly should call this instead to benefit from the
// reflex layer.
func AssignRoleWithReflex(
	rawInput string,
	m1Tier classify.ScopeTier,
	m1Pattern classify.ExecutionPattern,
	reflexes []Reflex,
	sessionID string,
	turnID string,
	logger MatchLogger,
) dispatch.RoleAssignment {
	match, ok := Match(rawInput, m1Tier, m1Pattern, reflexes)
	if !ok {
		// No reflex hit — fall through to existing AssignRole logic.
		return dispatch.AssignRole(m1Tier, m1Pattern)
	}

	// Convert the matched reflex to a RoleAssignment.
	assignment := reflexMatchToAssignment(match)

	// Log the match asynchronously-safe: logger errors are intentionally
	// swallowed so a transient DB failure never blocks the dispatch path.
	if logger != nil {
		excerpt := rawInput
		if len(excerpt) > 200 {
			excerpt = excerpt[:200]
		}
		_ = logger.LogReflexMatch(ReflexMatchEntry{
			SessionID:           sessionID,
			TurnID:              turnID,
			ReflexID:            match.Reflex.ID,
			Priority:            match.Reflex.Priority,
			Source:              "reflex",
			MatchedInputExcerpt: excerpt,
			HintTier:            match.HintTier.String(),
			HintPattern:         match.HintPattern.String(),
			ProfileSlug:         match.Reflex.ResolvesTo.Profile,
			Mode:                match.Reflex.SideEffects.ModeSignal,
			// This entry point performs no pre-dispatch rewrite, so raw and
			// sent text are identical — the writer collapses them to '' .
			RawInputText:  rawInput,
			SentInputText: rawInput,
		})
	}

	return assignment
}

// reflexMatchToAssignment converts a ReflexMatch to a dispatch.RoleAssignment.
// The reflex's Role string maps to dispatch.Role; the profile slug becomes the
// AgentSlug; DispatchVia maps to the Mode string.
//
// A non-empty ResolvesTo.WorkflowName takes precedence over Role/Profile,
// mirroring dispatch.ExecuteTask's own precedence (execute.go): a matched
// workflow reflex bypasses the Worker/Planner mapping entirely.
func reflexMatchToAssignment(m ReflexMatch) dispatch.RoleAssignment {
	if wf := m.Reflex.ResolvesTo.WorkflowName; wf != "" {
		return dispatch.RoleAssignment{
			Role:         dispatch.RoleWorkflow,
			WorkflowName: wf,
		}
	}

	role := roleFromString(m.Reflex.ResolvesTo.Role)

	slug := m.Reflex.ResolvesTo.Profile
	if slug == "" {
		// No profile override — use AssignRole's default slugs.
		if role == dispatch.RolePlanner {
			slug = dispatch.PlannerRoleSlug
		} else {
			slug = dispatch.WorkerRoleSlug
		}
	}

	mode := modeFromDispatchVia(m.Reflex.SideEffects.DispatchVia)

	return dispatch.RoleAssignment{
		Role:      role,
		AgentSlug: slug,
		Mode:      mode,
	}
}

// roleFromString maps the lowercase role string stored in a Reflex to a
// dispatch.Role constant. Unknown strings map to RoleWorker as a safe default.
func roleFromString(s string) dispatch.Role {
	switch s {
	case "planner":
		return dispatch.RolePlanner
	case "chat":
		return dispatch.RoleChat
	default:
		return dispatch.RoleWorker
	}
}

// modeFromDispatchVia maps the DispatchVia hint to the Mode string that
// dispatch.ExecuteTask / the spawner understands.
func modeFromDispatchVia(via string) string {
	switch via {
	case "executeBackground":
		return "async"
	case "executeTask":
		return "sync"
	default:
		return "sync"
	}
}

// MergeReflexes returns a single slice combining builtins and userOverrides,
// sorted by descending priority. When two reflexes share the same priority, the
// one with the lower index in its source slice wins (stable sort). User
// overrides are appended after builtins, so at equal priority a user override
// placed after a builtin will still win if priority >= 50 (per spec: "user
// entries with priority >= 50 reliably beat built-ins").
//
// The returned slice is owned by this function; the original slices are not
// modified.
func MergeReflexes(builtins, userOverrides []Reflex) []Reflex {
	merged := make([]Reflex, 0, len(builtins)+len(userOverrides))
	merged = append(merged, builtins...)
	merged = append(merged, userOverrides...)
	// Stable sort descending by priority so same-priority user overrides
	// (appended after builtins) appear after builtins — unless their priority
	// is higher, in which case they appear before. The spec says priority>=50
	// beats built-ins (max builtin priority is 25).
	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].Priority > merged[j].Priority
	})
	return merged
}
