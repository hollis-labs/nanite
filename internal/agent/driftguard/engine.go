package driftguard

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	pluginpkg "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/store"
)

type PluginHooks interface {
	ApplyFilter(name string, data interface{}, ctx pluginpkg.FilterContext) (interface{}, error)
	EmitReflexFired(sessionID string, data map[string]any)
	EmitReflexActionStaged(sessionID string, data map[string]any)
}

// Engine ties StateCollector + Evaluator + Executor together. Used by
// the monitor loop and the operator UI.
type Engine struct {
	Store     *store.Store
	Collector *StateCollector
	Executor  *Executor
	Logger    *slog.Logger
	Plugins   PluginHooks
}

// NewEngine builds a default-wired Engine. Hooks on the Executor stay
// nil until the caller (typically service.NewContainer) sets them.
func NewEngine(st *store.Store, logger *slog.Logger) *Engine {
	if logger == nil {
		logger = slog.Default()
	}
	return &Engine{
		Store:     st,
		Collector: &StateCollector{Store: st, Window: 5},
		Executor:  &Executor{Logger: logger},
		Logger:    logger,
	}
}

func (e *Engine) SetPluginHooks(h PluginHooks) {
	e.Plugins = h
}

// Evaluate is the per-tick entry point. It:
//  1. Collects fresh State for (sessionID, agentID, agentClass).
//  2. Lists active reflexes (class-base + agent-specific) and runs
//     each trigger predicate.
//  3. For every reflex whose trigger fires, invokes the Executor to
//     stage the action.
//  4. Bumps the reflex's fired_count + last_fired_at.
//
// Returns the AppliedActions slice so the caller (monitor loop) can
// translate inject_reminder bodies into <system-reminder> blocks on
// the next composed turn, and so the event-log layer can record what
// fired.
func (e *Engine) Evaluate(ctx context.Context, sessionID, agentID, agentClass string) (AppliedActions, error) {
	if e == nil {
		return AppliedActions{}, fmt.Errorf("Engine is nil")
	}

	state, err := e.Collector.Collect(ctx, sessionID, agentID, agentClass)
	if err != nil {
		return AppliedActions{}, fmt.Errorf("collect state: %w", err)
	}

	return e.EvaluateState(ctx, agentID, agentClass, state)
}

// EvaluateState runs the engine against a pre-built State. Exposed so
// tests can feed synthetic fixtures and the monitor loop can layer in
// MailUnreadCount / TickN / Events before the run.
func (e *Engine) EvaluateState(ctx context.Context, agentID, agentClass string, state State) (AppliedActions, error) {
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

	reflexes, err := e.Store.ListAgentReflexesForAgent(ctx, agentID, agentClass)
	if err != nil {
		return AppliedActions{}, fmt.Errorf("list reflexes: %w", err)
	}

	out := AppliedActions{
		Actions:       make([]AppliedAction, 0),
		FiredReflexes: make([]store.AgentReflex, 0),
	}
	now := time.Now()
	for _, r := range reflexes {
		fired, evalErr := EvaluateTrigger(r.TriggerKind, r.TriggerSpec, state)
		if evalErr != nil {
			e.Logger.Warn("reflex trigger evaluate failed",
				"session_id", state.SessionID,
				"reflex", r.Name,
				"err", evalErr,
			)
			continue
		}
		if !fired {
			continue
		}
		if recentlyFired(r, now, 15*time.Minute) {
			continue
		}
		action, err := e.Executor.Apply(ctx, r, state)
		if err != nil {
			e.Logger.Warn("reflex apply failed",
				"reflex", r.Name, "err", err)
			continue
		}
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
			e.Plugins.EmitReflexFired(state.SessionID, reflexEventData(agentID, agentClass, r, action, state))
			e.Plugins.EmitReflexActionStaged(state.SessionID, reflexEventData(agentID, agentClass, r, action, state))
		}
		out.Actions = append(out.Actions, action)
		out.FiredReflexes = append(out.FiredReflexes, r)
		// Best-effort fired_count bump; failure does not abort the run.
		if err := e.Store.BumpAgentReflexFired(ctx, r.ID, now); err != nil {
			e.Logger.Warn("reflex bump fired_count failed",
				"reflex", r.Name, "err", err)
		}
	}
	return out, nil
}

func reflexEventData(agentID, agentClass string, reflex store.AgentReflex, action AppliedAction, state State) map[string]any {
	return map[string]any{
		"agent_id":          agentID,
		"agent_class":       agentClass,
		"reflex_id":         reflex.ID,
		"reflex_name":       reflex.Name,
		"trigger_kind":      reflex.TriggerKind,
		"action_kind":       reflex.ActionKind,
		"priority":          reflex.Priority,
		"action":            action,
		"messages":          len(state.Messages),
		"user_messages":     len(state.UserMessages),
		"events":            len(state.Events),
		"mail_unread_count": state.MailUnreadCount,
		"tick_n":            state.TickN,
		"prefix_tokens":     state.PrefixTokens,
	}
}

func recentlyFired(r store.AgentReflex, now time.Time, window time.Duration) bool {
	if r.LastFiredAt == "" || window <= 0 {
		return false
	}
	ts, err := time.Parse(time.RFC3339, r.LastFiredAt)
	if err != nil {
		return false
	}
	return now.Sub(ts) >= 0 && now.Sub(ts) < window
}
