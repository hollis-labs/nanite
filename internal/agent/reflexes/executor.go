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
	case store.ReflexActionDispatchToAgent:
		// No live hook here — unlike Halt/Schedule/SendMessage,
		// dispatch_to_agent needs a stream channel + the chat service's
		// ToolService to synthesize a task_execute call and emit the
		// resulting envelope, neither of which this package depends on
		// (it would create an internal/agent/reflexes -> internal/service
		// import cycle). internal/service/chat_reflex_dispatch.go's
		// attemptReflexDispatch calls Apply() directly (not through
		// Engine.EvaluateState's debounced per-turn pass) to get the
		// parsed AppliedAction.Spec (agent_slug/confidence/reason), then
		// performs the dispatch itself. See that file and
		// TASKS/phase-4/02-dispatch-to-agent-reflex-action-kind-and-broker-migration.md
		// for why dispatch_to_agent is evaluated on its own path instead
		// of through the generic 15-minute recently-fired debounce every
		// other action kind goes through here: a routing decision is a
		// per-turn re-evaluation, not a nudge that should only fire once
		// per cooldown window.
		//
		// Comment-accuracy note (Phase 4 item 09,
		// TASKS/phase-4/09-fix-dispatch-to-agent-generic-pass-leak.md):
		// Engine.EvaluateState (engine.go) now skips dispatch_to_agent rows
		// entirely before they ever reach Apply, so this case is reached
		// ONLY via attemptReflexDispatch's direct Apply() call described
		// above — never through the generic per-turn pass. Before that
		// fix, EvaluateState's loop still called Apply() for a firing
		// dispatch_to_agent row, treated this no-op's nil error as a real
		// fire, and bumped fired_count/emitted plugin hooks for a dispatch
		// that never actually happened.
		return applied, nil
	default:
		return applied, fmt.Errorf("unknown action_kind %q", reflex.ActionKind)
	}
}
