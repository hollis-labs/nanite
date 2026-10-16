package reflexes

import (
	"context"
	"fmt"
	"time"

	pluginpkg "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/store"
)

// Private declared fixtures exercise the standalone resolver, host filters and
// telemetry contracts. This is a test-only algorithm harness, not a runtime
func (e *Engine) evaluatePrivateDeclaredFixtureState(ctx context.Context, agentID, agentClass string, state State) (AppliedActions, error) {
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

	reflexRows, err := privateFixtureCandidates(ctx, e.Store, agentID, agentClass, "", "")
	if err != nil {
		return AppliedActions{}, fmt.Errorf("list reflexes: %w", err)
	}

	candidates := make([]store.AgentReflex, 0, len(reflexRows))
	for _, r := range reflexRows {
		if r.ActionKind == store.ReflexActionDispatchToAgent || r.ActionKind == store.ReflexActionResumeLoopRun {
			continue
		}
		candidates = append(candidates, r)
	}

	now := time.Now()
	cooldownFn := func(r store.AgentReflex) bool {
		cooldown := EffectiveCooldown(e.ActionKindDefaultRecurrenceSeconds(r.ActionKind), r.RecurrenceOverrideSeconds)
		return RecentlyFired(r, now, cooldown)
	}
	kindLookup := func(_ context.Context, kind string) (*store.ReflexActionKind, error) {
		e.actionKindsMu.RLock()
		defer e.actionKindsMu.RUnlock()
		if k, ok := e.actionKinds[kind]; ok {
			return k, nil
		}
		return nil, fmt.Errorf("action kind %q not cached", kind)
	}

	resolved, outcomes, resolveErr := Resolve(ctx, candidates, state, e.Executor, cooldownFn, kindLookup)
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

	EmitFirings(ctx, privateFixtureTrace{e.Store}, e.Plugins, out, outcomes, state, FiringContext{
		AgentID:    agentID,
		AgentClass: agentClass,
	}, e.Logger)

	return out, nil
}
