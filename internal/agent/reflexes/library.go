package reflexes

import (
	"context"

	shared "github.com/hollis-labs/go-reflexes"
	"github.com/hollis-labs/nanite/internal/store"
)

// libraryState copies only host-collected signals. First-class live
// classification stays top-level; Nanite does not invent generic Attrs.
func libraryState(s State) shared.State {
	messages := make([]shared.MessageSignal, len(s.Messages))
	for i, m := range s.Messages {
		messages[i] = shared.MessageSignal(m)
	}
	users := make([]shared.MessageSignal, len(s.UserMessages))
	for i, m := range s.UserMessages {
		users[i] = shared.MessageSignal(m)
	}
	events := make([]shared.EventSignal, len(s.Events))
	for i, e := range s.Events {
		events[i] = shared.EventSignal(e)
	}
	return shared.State{
		SessionID: s.SessionID, AgentID: s.AgentID, AgentClass: s.AgentClass,
		Messages: messages, UserMessages: users, Events: events,
		MailUnreadCount: s.MailUnreadCount, TickN: s.TickN, PrefixTokens: s.PrefixTokens,
		ScopeTier: s.ScopeTier, ExecutionPattern: s.ExecutionPattern,
	}
}

// Struct conversions require compatible persisted field layouts at compile
// time, including provenance and workflow scope. Candidate selection stays in
// the host store, before these rows reach the library.
func libraryReflexes(rows []store.AgentReflex) []shared.Reflex {
	if rows == nil {
		return nil
	}
	result := make([]shared.Reflex, len(rows))
	for i, r := range rows {
		result[i] = shared.Reflex(r)
	}
	return result
}

func libraryApplied(a AppliedActions) shared.AppliedActions {
	var actions []shared.AppliedAction
	if a.Actions != nil {
		actions = make([]shared.AppliedAction, len(a.Actions))
		for i, action := range a.Actions {
			actions[i] = shared.AppliedAction(action)
		}
	}
	return shared.AppliedActions{Actions: actions, FiredReflexes: libraryReflexes(a.FiredReflexes)}
}

func hostApplied(a shared.AppliedActions) AppliedActions {
	var actions []AppliedAction
	if a.Actions != nil {
		actions = make([]AppliedAction, len(a.Actions))
		for i, action := range a.Actions {
			actions[i] = AppliedAction(action)
		}
	}
	var rows []store.AgentReflex
	if a.FiredReflexes != nil {
		rows = make([]store.AgentReflex, len(a.FiredReflexes))
		for i, r := range a.FiredReflexes {
			rows[i] = store.AgentReflex(r)
		}
	}
	return AppliedActions{Actions: actions, FiredReflexes: rows}
}

// Each pass gets an adapter over the caller's current hooks. Effects run at
// Resolve time and keep Nanite's error/log behavior; dispatch and loop resume
// are staged decisions whose real lifecycle belongs to their call sites.
func libraryExecutor(e *Executor, state State) *shared.Executor {
	if e == nil {
		return nil
	}
	x := shared.NewExecutor(e.Logger)
	x.Stage(shared.ActionResumeLoopRun)
	for _, kind := range []string{shared.ActionHaltSession, shared.ActionAddSchedule, shared.ActionSendMessage} {
		x.Handle(kind, shared.HandlerFunc(func(ctx context.Context, f shared.Firing) error {
			_, err := e.applySpec(ctx, store.AgentReflex(f.Reflex), state, f.Spec)
			return err
		}))
	}
	return x
}
