package reflexes

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/store"
)

// HaltHook is an optional callback that the executor invokes when a
// reflex action_kind = 'halt_session' fires. Nil = no live halt; the
// action is still staged in the AppliedActions slice + event_log so
// downstream consumers can react. Returning an error from the hook is
// logged but does not abort the reflex run.
type HaltHook func(ctx context.Context, sessionID, reason string, evidence map[string]interface{}) error

// ScheduleHook is invoked when a reflex action_kind='add_schedule'
// fires — it inserts a new row into agent_schedules with the spec
// fields. Nil = no live insert (the action is logged-only).
type ScheduleHook func(ctx context.Context, agentID string, spec map[string]interface{}) error

// SendMessageHook is invoked when a reflex action_kind='send_message'
// fires — typically used by wake_on_mail or notification reflexes.
// Nil = no live send (logged only).
type SendMessageHook func(ctx context.Context, fromAgentID string, spec map[string]interface{}) error

// Executor wires action kinds to their effect surfaces. All hooks are
// optional; missing hooks simply skip the live application (the action
// still appears in AppliedActions for tracing/event_log).
type Executor struct {
	Halt        HaltHook
	Schedule    ScheduleHook
	SendMessage SendMessageHook
	// Logger is used for executor diagnostics. Required.
	Logger *slog.Logger
}

// Apply translates a fired reflex into a concrete AppliedAction and
// invokes the relevant live hook (if any). The AppliedAction always
// makes it into the returned slice so the caller can log/event the
// firing even when no hook is wired.
func (e *Executor) Apply(ctx context.Context, reflex store.AgentReflex, state State) (AppliedAction, error) {
	spec := map[string]interface{}{}
	if reflex.ActionSpec != "" {
		if err := json.Unmarshal([]byte(reflex.ActionSpec), &spec); err != nil {
			return AppliedAction{}, fmt.Errorf("parse action_spec: %w", err)
		}
	}
	applied := AppliedAction{
		ReflexID:   reflex.ID,
		ReflexName: reflex.Name,
		ActionKind: reflex.ActionKind,
		Spec:       spec,
	}

	switch reflex.ActionKind {
	case store.ReflexActionInjectReminder:
		// No live hook needed — the monitor-loop integration picks up
		// the AppliedAction.Spec.body and prepends it as a
		// <system-reminder> on the next composed turn.
		return applied, nil
	case store.ReflexActionForceToolChoice:
		// Same: the composer reads AppliedAction.Spec.tool_name and
		// sets next call's tool_choice. Spike has no live hook here.
		return applied, nil
	case store.ReflexActionHaltSession:
		reason, _ := spec["reason"].(string)
		if reason == "" {
			reason = "reflex " + reflex.Name + " fired"
		}
		if e.Halt != nil {
			evidence := map[string]interface{}{
				"reflex_id":   reflex.ID,
				"reflex_name": reflex.Name,
			}
			if err := e.Halt(ctx, state.SessionID, reason, evidence); err != nil {
				e.Logger.Warn("reflex halt hook failed",
					"session_id", state.SessionID,
					"reflex", reflex.Name,
					"err", err,
				)
			}
		} else {
			e.Logger.Info("reflex halt staged (no halt hook wired)",
				"session_id", state.SessionID, "reflex", reflex.Name)
		}
		return applied, nil
	case store.ReflexActionAddSchedule:
		if e.Schedule != nil {
			if err := e.Schedule(ctx, reflex.AgentID, spec); err != nil {
				e.Logger.Warn("reflex schedule hook failed",
					"reflex", reflex.Name, "err", err)
			}
		}
		return applied, nil
	case store.ReflexActionSendMessage:
		if e.SendMessage != nil {
			if err := e.SendMessage(ctx, reflex.AgentID, spec); err != nil {
				e.Logger.Warn("reflex send_message hook failed",
					"reflex", reflex.Name, "err", err)
			}
		}
		return applied, nil
	default:
		return applied, fmt.Errorf("unknown action_kind %q", reflex.ActionKind)
	}
}
