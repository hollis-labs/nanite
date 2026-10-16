package reflexes

// Test-only pre-adoption Nanite reference, preserved from e5415d31.
// It must stay independent of go-reflexes; production wrappers are compared
// against these algorithms using isolated host stores and real emitted traces.

// TASKS/reflex-taxonomy/03-shared-decision-engine.md — the one shared
// decision primitive docs/engineering/architecture/
// 10-reflex-action-taxonomy.md calls "one shared decision engine, multiple
// legitimate invocation points." Encodes Facet 2's per-action-kind
// combining algorithm (deny_overrides / first_applicable / all_applicable,
// modeled on XACML's policy-combining algorithms). Every call site that
// evaluates a set of private_declared_reflexes candidates against a State and decides
// which fired ones actually get applied calls referenceResolve() instead of
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
// list before calling referenceResolve — referenceResolve itself has no opinion about that
// exclusion, it is a property of the caller, not of this primitive).

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"github.com/hollis-labs/nanite/internal/store"
)

func referenceResolve(
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
		fired, evalErr := referenceEvaluateTrigger(r.TriggerKind, r.TriggerSpec, state)
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

	// referenceResolve each present kind's combining_algorithm (and, TASKS/
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
		referenceApplyOne(ctx, exec, candidates[winner], state, &out, outcomes, winner)
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
			referenceApplyOne(ctx, exec, candidates[i], state, &out, outcomes, i)
		}
	}
	return out, outcomes, nil
}

// referenceApplyOne calls exec.Apply for a selected candidate and records the
// result — either appending to out.Actions/out.FiredReflexes and marking
// outcomes[idx].Selected, or recording outcomes[idx].ApplyError. An apply
// failure is per-candidate and does not propagate as a referenceResolve() error,
// matching every existing call site's own "log a warning and continue"
// handling of an Executor.Apply failure.
func referenceApplyOne(ctx context.Context, exec *Executor, r store.AgentReflex, state State, out *AppliedActions, outcomes []CandidateOutcome, idx int) {
	action, err := exec.Apply(ctx, r, state)
	if err != nil {
		outcomes[idx].ApplyError = err.Error()
		return
	}
	outcomes[idx].Selected = true
	out.Actions = append(out.Actions, action)
	out.FiredReflexes = append(out.FiredReflexes, r)
}
