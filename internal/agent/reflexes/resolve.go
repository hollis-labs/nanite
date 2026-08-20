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
	"fmt"
	"log/slog"
	"sort"

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
	out := AppliedActions{
		Actions:       make([]AppliedAction, 0),
		FiredReflexes: make([]store.AgentReflex, 0),
	}
	outcomes := make([]CandidateOutcome, len(candidates))

	if exec == nil {
		return out, outcomes, fmt.Errorf("reflexes.Resolve: nil Executor")
	}

	// Step 1+2: evaluate every candidate's trigger, then its cooldown
	// eligibility. eligibleByKind preserves the candidates slice's own
	// given order within each kind's bucket.
	eligibleByKind := make(map[string][]int)
	for i, r := range candidates {
		oc := CandidateOutcome{
			ReflexID:   r.ID,
			ReflexName: r.Name,
			ActionKind: r.ActionKind,
			Priority:   r.Priority,
			CreatedAt:  r.CreatedAt,
		}
		fired, evalErr := EvaluateTrigger(r.TriggerKind, r.TriggerSpec, state)
		if evalErr != nil {
			oc.TriggerError = evalErr.Error()
			outcomes[i] = oc
			continue
		}
		oc.TriggerFired = fired
		if !fired {
			outcomes[i] = oc
			continue
		}
		if cooldownFn != nil && cooldownFn(r) {
			oc.CooldownSuppressed = true
			outcomes[i] = oc
			continue
		}
		oc.Eligible = true
		outcomes[i] = oc
		eligibleByKind[r.ActionKind] = append(eligibleByKind[r.ActionKind], i)
	}

	if len(eligibleByKind) == 0 {
		return out, outcomes, nil
	}

	// Resolve each present kind's combining_algorithm (and, TASKS/
	// reflex-taxonomy/06-unified-reflex-telemetry.md, its category)
	// exactly once.
	algoByKind := make(map[string]string, len(eligibleByKind))
	for kind := range eligibleByKind {
		algo := "all_applicable"
		category := ""
		if kindLookup != nil {
			switch k, err := kindLookup(ctx, kind); {
			case err != nil:
				// TASKS/reflex-taxonomy/08-fix-resolve-fail-open-visibility.md:
				// a kindLookup error (e.g. engine.go's kindLookup closure's
				// own "action kind %q not cached") used to be swallowed
				// here with zero logging anywhere in the chain. Surfaced
				// now so a transient cache/DB hiccup that fails a
				// deny_overrides kind (halt_session) open to
				// all_applicable is operator-visible, not silent.
				slog.Warn("reflexes.Resolve: action-kind lookup failed, defaulting combining_algorithm to all_applicable",
					"session_id", state.SessionID,
					"action_kind", kind,
					"err", err,
				)
			case k == nil || k.CombiningAlgorithm == "":
				// Same fail-open, different cause: the lookup succeeded
				// but returned no usable combining_algorithm (e.g. a
				// reflex_action_kinds row edited directly with an empty
				// value — no CRUD surface validates this column).
				slog.Warn("reflexes.Resolve: action-kind resolved with empty combining_algorithm, defaulting to all_applicable",
					"session_id", state.SessionID,
					"action_kind", kind,
				)
				if k != nil {
					category = k.Category
				}
			default:
				algo = k.CombiningAlgorithm
				category = k.Category
			}
		}
		algoByKind[kind] = algo
		for _, i := range eligibleByKind[kind] {
			outcomes[i].CombiningAlgorithm = algo
			outcomes[i].Category = category
		}
	}

	tieBreakLess := func(a, b store.AgentReflex) bool {
		if a.Priority != b.Priority {
			return a.Priority > b.Priority
		}
		return a.CreatedAt < b.CreatedAt
	}

	// Step 3a: deny_overrides short-circuit — collected across every
	// deny_overrides kind present this pass (only halt_session is seeded
	// as deny_overrides today, but this generalizes correctly if a future
	// kind is too: the single highest-priority candidate among the whole
	// union wins outright and nothing else this pass applies).
	var denyIdx []int
	for kind, idxs := range eligibleByKind {
		if algoByKind[kind] == "deny_overrides" {
			denyIdx = append(denyIdx, idxs...)
		}
	}
	if len(denyIdx) > 0 {
		sort.SliceStable(denyIdx, func(a, b int) bool {
			return tieBreakLess(candidates[denyIdx[a]], candidates[denyIdx[b]])
		})
		winner := denyIdx[0]
		applyOne(ctx, exec, candidates[winner], state, &out, outcomes, winner)
		return out, outcomes, nil
	}

	// Step 3b: no deny_overrides kind fired — first_applicable /
	// all_applicable selection, per kind.
	selected := make(map[int]bool, len(candidates))
	for kind, idxs := range eligibleByKind {
		switch algoByKind[kind] {
		case "first_applicable":
			sorted := append([]int(nil), idxs...)
			sort.SliceStable(sorted, func(a, b int) bool {
				return tieBreakLess(candidates[sorted[a]], candidates[sorted[b]])
			})
			selected[sorted[0]] = true
		default:
			// all_applicable, and any unrecognized/legacy value — fail
			// open the same way an unresolvable kind does above.
			for _, i := range idxs {
				selected[i] = true
			}
		}
	}

	// Step 4: apply every selected candidate, in the candidates slice's
	// own given order (matching every existing call site's prior
	// behavior of applying fired reflexes in priority-then-created_at
	// order regardless of action_kind).
	for i := range candidates {
		if selected[i] {
			applyOne(ctx, exec, candidates[i], state, &out, outcomes, i)
		}
	}
	return out, outcomes, nil
}

// applyOne calls exec.Apply for a selected candidate and records the
// result — either appending to out.Actions/out.FiredReflexes and marking
// outcomes[idx].Selected, or recording outcomes[idx].ApplyError. An apply
// failure is per-candidate and does not propagate as a Resolve() error,
// matching every existing call site's own "log a warning and continue"
// handling of an Executor.Apply failure.
func applyOne(ctx context.Context, exec *Executor, r store.AgentReflex, state State, out *AppliedActions, outcomes []CandidateOutcome, idx int) {
	action, err := exec.Apply(ctx, r, state)
	if err != nil {
		outcomes[idx].ApplyError = err.Error()
		return
	}
	outcomes[idx].Selected = true
	out.Actions = append(out.Actions, action)
	out.FiredReflexes = append(out.FiredReflexes, r)
}
