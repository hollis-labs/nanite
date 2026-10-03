package reflexes

// TASKS/reflex-taxonomy/03-shared-decision-engine.md — the one shared
// decision primitive docs/engineering/architecture/
// 10-reflex-action-taxonomy.md calls "one shared decision engine, multiple
// legitimate invocation points." Encodes Facet 2's per-action-kind
// combining algorithm (deny_overrides / first_applicable / all_applicable,
// modeled on XACML's policy-combining algorithms). Every call site that
// evaluates a set of agent_reflexes candidates against a State and decides
// which fired ones actually get applied calls Resolve() instead of
// hand-rolling its own priority-ordering/first-wins/all-fire loop:
//   - Engine.EvaluateState (engine.go) — the generic per-turn pass.
//   - attemptReflexDispatch (internal/service/chat_reflex_dispatch.go) —
//     dispatch_to_agent, from the main chat turn loop.
//   - matchDispatchToAgentReflex (internal/mcp/self_tools_dispatch.go) —
//     dispatch_to_agent, from the task_execute self-tool path.
//
// What does NOT unify (see the architecture doc's "One shared decision
// engine, multiple legitimate invocation points" section): how State gets
// built, and which candidate rows a caller chooses to pass in (e.g.
// EvaluateState filters dispatch_to_agent rows out of its own candidate
// list before calling Resolve — Resolve itself has no opinion about that
// exclusion, it is a property of the caller, not of this primitive).

import (
	"context"

	shared "github.com/hollis-labs/go-reflexes"
	"github.com/hollis-labs/nanite/internal/store"
)

// ActionKindLookup resolves one reflex_action_kinds row by name (one of the
// store.ReflexAction* constants) — the Facet 2 combining-algorithm lookup
// Resolve needs per distinct action_kind present in a candidate set. Passed
// in rather than hardwired to a concrete *store.Store so each call site can
// supply its own resolution strategy: Engine.EvaluateState reads its own
// in-memory action-kind cache (engine.go's actionKinds, refreshed via
// RefreshActionKindCache), while attemptReflexDispatch and
// matchDispatchToAgentReflex — which have no Engine of their own to cache
// through — resolve it via a direct store.GetReflexActionKind call, the
// same pattern both already used pre-this-task for the Facet 4 recurrence
// lookup.
type ActionKindLookup func(ctx context.Context, actionKind string) (*store.ReflexActionKind, error)

// CooldownFunc reports whether reflex r is currently suppressed by its
// Facet 4 recurrence cooldown (system default -> kind-level override ->
// reflex-level override, resolved via EffectiveCooldown/RecentlyFired,
// recurrence.go). true = suppressed: r's trigger may have fired, but it is
// not eligible to be selected this pass.
type CooldownFunc func(r store.AgentReflex) bool

// CandidateOutcome is the per-candidate detail of one Resolve() pass — one
// entry per candidate Resolve was given, whether or not it fired or was
// selected. Deliberately carries more than the minimum a single caller
// needs today: fired-or-not, why-not-eligible (trigger-false vs
// cooldown-suppressed — two distinct facts, per this task's own brief), the
// combining algorithm applied, and whether the candidate was ultimately
// selected/applied — enough for a later telemetry consumer (see
// docs/engineering/architecture/10-reflex-action-taxonomy.md's Telemetry
// section) to build an alternatives_considered-style record without
// re-running any evaluation.
type CandidateOutcome struct {
	ReflexID   string `json:"reflex_id"`
	ReflexName string `json:"reflex_name"`
	ActionKind string `json:"action_kind"`
	Priority   int64  `json:"priority"`
	CreatedAt  string `json:"created_at"`

	// TriggerFired is EvaluateTrigger's raw result for this candidate.
	// false both when the trigger genuinely evaluated false and when
	// evaluation errored (see TriggerError) — matching every existing
	// call site's own "an eval error is non-fatal, treat as not fired"
	// handling.
	TriggerFired bool `json:"trigger_fired"`
	// TriggerError carries EvaluateTrigger's error message, if any. Empty
	// on a clean evaluation (fired or not).
	TriggerError string `json:"trigger_error,omitempty"`

	// CooldownSuppressed is true when TriggerFired is true but r's Facet 4
	// cooldown had not yet elapsed — "not eligible," a fact distinct from
	// "trigger false."
	CooldownSuppressed bool `json:"cooldown_suppressed"`

	// Eligible is true iff TriggerFired && !CooldownSuppressed. Only
	// eligible candidates are grouped by action_kind and run through their
	// kind's combining algorithm.
	Eligible bool `json:"eligible"`

	// CombiningAlgorithm is the reflex_action_kinds.combining_algorithm
	// this candidate's action_kind resolved to. Empty when the candidate
	// was never eligible (its kind's algorithm was never looked up).
	CombiningAlgorithm string `json:"combining_algorithm,omitempty"`

	// Category is the reflex_action_kinds.category (Facet 1,
	// system_message vs execute_action) this candidate's action_kind
	// resolved to — added by TASKS/reflex-taxonomy/
	// 06-unified-reflex-telemetry.md so EmitFirings (telemetry.go) can
	// fold it into the unified trace record without a second kind
	// lookup. Same availability rule as CombiningAlgorithm: populated
	// only for a candidate that reached the kind-resolution step (i.e.
	// was eligible), and only when kindLookup returned a non-nil row —
	// empty on a kindLookup error, even though CombiningAlgorithm still
	// gets its all_applicable fail-open default in that case (Category
	// has no equivalent meaningful default to fail open to).
	Category string `json:"category,omitempty"`

	// Selected is true iff this candidate's action actually made it into
	// the returned AppliedActions — it survived its kind's combining
	// algorithm (and, for a pass where a deny_overrides kind fired,
	// wasn't preempted by that kind's winner).
	Selected bool `json:"selected"`

	// ApplyError carries Executor.Apply's error message when a selected
	// candidate's action failed to apply — chosen by the combining
	// algorithm, but did not make it into AppliedActions. Matches every
	// existing call site's own "log and skip" handling of an Apply error.
	ApplyError string `json:"apply_error,omitempty"`
}

// Resolve is the one shared decision primitive: given a list of reflex
// candidates already scoped to one (agent_id, class_tag) evaluation set
// (the caller's job — see Store.ListAgentReflexesForAgent), a State to
// evaluate triggers against, an Executor to apply selected actions, a
// CooldownFunc to resolve the Facet 4 recurrence cascade, and an
// ActionKindLookup to resolve the Facet 2 combining algorithm per
// action_kind — decides which fired candidates actually get applied this
// pass, applies them, and returns both the applied actions and full
// per-candidate detail.
//
// Algorithm (Facet 2, docs/engineering/architecture/
// 10-reflex-action-taxonomy.md):
//  1. Evaluate every candidate's trigger (EvaluateTrigger, unchanged).
//  2. A candidate whose trigger fires is then checked against cooldownFn;
//     if suppressed, it is "not eligible" — distinct from "trigger false"
//     (CandidateOutcome.CooldownSuppressed).
//  3. Eligible candidates are grouped by action_kind. Each present kind's
//     combining_algorithm (via kindLookup) decides selection:
//     - deny_overrides: collected across every deny_overrides kind
//     present this pass; the single highest-priority candidate among
//     that union wins outright (tie-break priority DESC, created_at
//     ASC, matching Store.ListAgentReflexesForAgent's own order) and
//     short-circuits the whole pass — no other kind's actions apply.
//     - first_applicable: the highest-priority eligible candidate of
//     this kind is selected (same tie-break).
//     - all_applicable: every eligible candidate of this kind is
//     selected.
//  4. Each selected candidate is applied via exec.Apply, in the same
//     relative order the candidates slice was given in (itself expected to
//     already be priority DESC/created_at ASC per every real caller's use
//     of ListAgentReflexesForAgent).
//
// A kind lookup failure (kindLookup returns an error, or nil is passed)
// degrades that kind to "all_applicable" — today's universal
// pre-taxonomy behavior — rather than erroring the whole pass, the same
// fail-open posture EffectiveCooldown/ActionKindDefaultRecurrenceSeconds
// already use for an unresolvable kind. TASKS/reflex-taxonomy/
// 08-fix-resolve-fail-open-visibility.md: this fail-open is otherwise
// silent (deny_overrides — halt_session's own kind — is exactly the
// combining algorithm this defaults away from), so both a lookup error
// and a resolved-but-empty CombiningAlgorithm are Warn-logged here, via
// the package-level slog logger, naming the action kind and (for a
// lookup error) the error itself. This is the one place all three
// Resolve() call sites' kindLookup failures funnel through, including
// engine.go's kindLookup closure, whose own "action kind %q not cached"
// error used to be constructed and silently discarded by the caller-side
// err==nil check this replaces.
//
// A per-candidate Executor.Apply failure is recorded on that candidate's
// CandidateOutcome.ApplyError and the candidate is simply not added to
// AppliedActions — it does not abort the rest of the pass, matching every
// existing call site's own "log a warning and continue" handling. Resolve
// itself returns a non-nil error only for a genuine caller-programming
// error (a nil Executor), not for any per-candidate evaluation/apply
// failure.
func Resolve(
	ctx context.Context,
	candidates []store.AgentReflex,
	state State,
	exec *Executor,
	cooldownFn CooldownFunc,
	kindLookup ActionKindLookup,
) (AppliedActions, []CandidateOutcome, error) {
	var cooldown shared.CooldownFunc
	if cooldownFn != nil {
		cooldown = func(r shared.Reflex) bool { return cooldownFn(store.AgentReflex(r)) }
	}
	var lookup shared.ActionKindLookup
	if kindLookup != nil {
		lookup = func(ctx context.Context, kind string) (*shared.ActionKind, error) {
			k, err := kindLookup(ctx, kind)
			if k == nil {
				return nil, err
			}
			v := shared.ActionKind(*k)
			return &v, err
		}
	}
	// Resolve invokes the library evaluator directly, so apply the same host
	// vocabulary gate here as in EvaluateTrigger. Rejected rows still occupy
	// their original outcome positions and appear as non-fired alternatives.
	rows := make([]shared.Reflex, 0, len(candidates))
	indexes := make([]int, 0, len(candidates))
	outcomes := make([]CandidateOutcome, len(candidates))
	for i, r := range candidates {
		// A nil executor is a caller error checked before trigger evaluation.
		if exec != nil {
			if err := unsupportedHostPredicate(r.TriggerKind, r.TriggerSpec); err != nil {
				outcomes[i] = CandidateOutcome{ReflexID: r.ID, ReflexName: r.Name,
					ActionKind: r.ActionKind, Priority: r.Priority, CreatedAt: r.CreatedAt,
					TriggerError: err.Error()}
				continue
			}
		}
		rows = append(rows, shared.Reflex(r))
		indexes = append(indexes, i)
	}
	applied, considered, err := shared.Resolve(ctx, rows, libraryState(state), libraryExecutor(exec, state), cooldown, lookup)
	for i, oc := range considered {
		outcomes[indexes[i]] = CandidateOutcome(oc)
	}
	return hostApplied(applied), outcomes, err
}
