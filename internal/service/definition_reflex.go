package service

import (
	"context"
	"fmt"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/store"
)

// evaluateDefinitionReflexes takes candidates only from pinned intrinsic content.
// The collector reads session signals, not historical profile/reflex candidates.
// Host effects remain limited to this owned session; tool preferences cannot
// confer membership in the granted tool roster.
func (s *chatServiceImpl) evaluateDefinitionReflexes(ctx context.Context, session *store.Session, agent *store.AgentProfile, slots *SlotAssemblyResult) ([]reflexes.AppliedAction, error) {
	if agent.DefinitionReflex == nil {
		return nil, nil
	}
	st, ok := s.store.(*store.Store)
	if !ok {
		return nil, fmt.Errorf("pinned reflex state collector unavailable")
	}
	state, err := (&reflexes.StateCollector{Store: st, Window: 128}).Collect(ctx, session.ID, "", agent.Class)
	if err != nil {
		st.LogEvent(context.WithoutCancel(ctx), session.ID, "reflex_eval_error", "reflex", err.Error(), "{}")
		return nil, fmt.Errorf("evaluate pinned reflex policy: %w", err)
	}
	fired, err := agent.DefinitionReflex.Evaluate(ctx, reflexes.CollectedState(state))
	if err != nil {
		st.LogEvent(context.WithoutCancel(ctx), session.ID, "reflex_eval_error", "reflex", err.Error(), "{}")
		return nil, fmt.Errorf("evaluate pinned reflex policy: %w", err)
	}
	var actions []reflexes.AppliedAction
	for _, rule := range fired {
		spec := map[string]interface{}{}
		switch rule.Action.Kind {
		case store.ReflexActionHaltSession:
			spec["reason"] = rule.Action.Reason
			if err = st.MarkSessionHalted(ctx, session.ID, rule.Action.Reason); err != nil { // still stop this turn even when outcome bookkeeping fails
				st.LogEvent(context.WithoutCancel(ctx), session.ID, "reflex_effect_error", "reflex", err.Error(), "{}")
			}
		case store.ReflexActionInjectReminder:
			spec["body"] = rule.Action.Body
		case store.ReflexActionForceToolChoice:
			// No verified actor tool grant port is adopted yet. A pin is not a grant.
			// Refuse this request rather than advertise an ungranted tool in a reminder.
			st.LogEvent(ctx, session.ID, "reflex_effect_refused", "reflex", fmt.Sprintf("tool preference %s requires verified actor grant", rule.Action.ToolName), "{}")
			continue
		}
		actions = append(actions, reflexes.AppliedAction{ReflexID: rule.ID, ReflexName: rule.Name, ActionKind: rule.Action.Kind, Spec: spec})
		st.LogEvent(ctx, session.ID, "definition_reflex_fired", "reflex", rule.Name, "{}")
	}
	appendUserContext(slots, formatReflexReminder(actions))
	return actions, nil
}
