package reflexes

// Test-only pre-adoption Nanite reference, preserved from e5415d31.
// It must stay independent of go-reflexes; production wrappers are compared
// against these algorithms using isolated host stores and real emitted traces.

import (
	"context"
	"fmt"
	"time"

	pluginpkg "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/store"
)

func (e *Engine) referenceEvaluateState(ctx context.Context, agentID, agentClass string, state State) (AppliedActions, error) {
	if e.Plugins != nil {
		filtered, err := e.Plugins.ApplyFilter(pluginpkg.FilterReflexState, state, pluginpkg.FilterContext{
			SessionID: state.SessionID,
			AgentID:   agentID,
			Metadata: map[string]interface{}{
				"agent_class": agentClass,
			},
		})
		if err != nil {
			e.Logger.Warn("reflex state filter failed", "err", err)
		} else if filteredState, ok := filtered.(State); ok {
			state = filteredState
		}
	}

	reflexRows, err := declaredFixture(e.Store).ListAgentReflexesForAgent(ctx, agentID, agentClass)
	if err != nil {
		return AppliedActions{}, fmt.Errorf("list reflexes: %w", err)
	}

	// Phase 4 item 09
	// (TASKS/phase-4/09-fix-dispatch-to-agent-generic-pass-leak.md):
	// dispatch_to_agent rows are architecturally evaluated exclusively
	// through the dedicated attemptReflexDispatch
	// (internal/service/chat_reflex_dispatch.go) and
	// matchDispatchToAgentReflex (internal/mcp/self_tools_dispatch.go)
	// call sites — both call Store.ListAgentReflexesForAgent and
	// referenceResolve()/Executor.Apply directly, never through this generic
	// per-turn pass. Executor.Apply's dispatch_to_agent case is an
	// explicit, documented no-op (see executor.go) because a real dispatch
	// needs a stream channel + ToolService this package deliberately
	// doesn't depend on. Without this skip, that no-op still returns
	// (applied, nil), and referenceResolve() would treat it as a real fire: bump
	// fired_count, write a redundant event_log row (via
	// evaluateAndInjectReflexes), and emit
	// EmitReflexFired/EmitReflexActionStaged with no dispatch having
	// actually occurred — inflating telemetry and giving plugins a false
	// "this routing reflex fired" signal. Filter BEFORE calling referenceResolve —
	// a dispatch_to_agent row should be entirely invisible to this pass,
	// not just short-circuited after being evaluated. This is a property
	// of what THIS caller passes to referenceResolve(), not something referenceResolve()
	// itself needs to know about (TASKS/reflex-taxonomy/
	// 03-shared-decision-engine.md's own instruction). This does not
	// affect the query itself — Store.ListAgentReflexesForAgent still
	// returns dispatch_to_agent rows; the dedicated call sites above share
	// that same query and need them.
	//
	// TASKS/loops/11-loop-event-predicate-trigger.md: resume_loop_run rows
	// are filtered out here for the identical reason, not merely a similar
	// one — Executor.Apply's resume_loop_run case (executor.go) is also a
	// documented no-op, because the real effect (LoopEngine.Resume) needs
	// internal/loop, which this package cannot import without cycling
	// through internal/service back to this package. resume_loop_run's own
	// real evaluation cadence is internal/service/loop_resume_reflex.go's
	// EvaluateLoopRunResumeReflexes (a dedicated call site, mirroring
	// attemptReflexDispatch), driven in practice by a scheduled tick, not
	// this per-turn pass — see that task's Work Log for why the per-turn
	// pass is structurally the wrong cadence for this kind (a WAIT-parked
	// LoopRun routinely has no live chat turn to piggyback on).
	candidates := make([]store.AgentReflex, 0, len(reflexRows))
	for _, r := range reflexRows {
		if r.ActionKind == store.ReflexActionDispatchToAgent || r.ActionKind == store.ReflexActionResumeLoopRun {
			continue
		}
		candidates = append(candidates, r)
	}

	now := time.Now()
	cooldownFn := func(r store.AgentReflex) bool {
		cooldown := referenceEffectiveCooldown(e.ActionKindDefaultRecurrenceSeconds(r.ActionKind), r.RecurrenceOverrideSeconds)
		return referenceRecentlyFired(r, now, cooldown)
	}
	// TASKS/reflex-taxonomy/08-fix-resolve-fail-open-visibility.md: this
	// closure's own "action kind %q not cached" error used to be built
	// here and then silently discarded by referenceResolve()'s (formerly bare)
	// err==nil check — zero log line anywhere in the chain. referenceResolve()
	// itself now Warn-logs any kindLookup error it receives (including
	// this one) before falling open to all_applicable, so the error
	// constructed below is no longer lost; nothing in this closure needed
	// to change beyond this note.
	kindLookup := func(_ context.Context, kind string) (*store.ReflexActionKind, error) {
		e.actionKindsMu.RLock()
		defer e.actionKindsMu.RUnlock()
		if k, ok := e.actionKinds[kind]; ok {
			return k, nil
		}
		return nil, fmt.Errorf("action kind %q not cached", kind)
	}

	resolved, outcomes, resolveErr := referenceResolve(ctx, candidates, state, e.Executor, cooldownFn, kindLookup)
	if resolveErr != nil {
		return AppliedActions{}, fmt.Errorf("resolve reflexes: %w", resolveErr)
	}
	for _, oc := range outcomes {
		if oc.TriggerError != "" {
			e.Logger.Warn("reflex trigger evaluate failed",
				"session_id", state.SessionID,
				"reflex", oc.ReflexName,
				"err", oc.TriggerError,
			)
		}
		if oc.ApplyError != "" {
			e.Logger.Warn("reflex apply failed",
				"reflex", oc.ReflexName, "err", oc.ApplyError)
		}
	}

	out := AppliedActions{
		Actions:       make([]AppliedAction, 0, len(resolved.Actions)),
		FiredReflexes: make([]store.AgentReflex, 0, len(resolved.FiredReflexes)),
	}
	for i, action := range resolved.Actions {
		r := resolved.FiredReflexes[i]
		if e.Plugins != nil {
			filtered, err := e.Plugins.ApplyFilter(pluginpkg.FilterReflexAction, action, pluginpkg.FilterContext{
				SessionID: state.SessionID,
				AgentID:   agentID,
				Metadata: map[string]interface{}{
					"agent_class": agentClass,
					"reflex_id":   r.ID,
					"reflex_name": r.Name,
				},
			})
			if err != nil {
				e.Logger.Warn("reflex action filter failed", "reflex", r.Name, "err", err)
			} else if filteredAction, ok := filtered.(AppliedAction); ok {
				action = filteredAction
			}
		}
		out.Actions = append(out.Actions, action)
		out.FiredReflexes = append(out.FiredReflexes, r)
	}

	// TASKS/reflex-taxonomy/06-unified-reflex-telemetry.md: the event_log
	// trace write (formerly one layer up, in chat_reflexes.go's
	// evaluateAndInjectReflexes), the fired_count/last_fired_at bump, and
	// the EmitReflexFired/EmitReflexActionStaged plugin hooks (formerly
	// inline in the loop above) are now one centralized call — the same
	// referenceEmitFirings attemptReflexDispatch and matchDispatchToAgentReflex go
	// through — operating on `out` (already FilterReflexAction-filtered
	// above, so hook/trace payloads see the same post-filter action data
	// they did before this change) and `outcomes` (referenceResolve()'s own
	// per-candidate detail, for AlternativesConsidered/Category).
	referenceEmitFirings(ctx, privateFixtureTrace{e.Store}, e.Plugins, out, outcomes, state, FiringContext{
		AgentID:    agentID,
		AgentClass: agentClass,
	}, e.Logger)

	return out, nil
}
