package reflexes

// TASKS/reflex-taxonomy/03-shared-decision-engine.md's Done-means regression
// test: "a halt_session reflex and an unrelated inject_reminder reflex both
// fire in the same pass -> Resolve()'s output contains only the halt
// action, not both." resolve_test.go's
// TestResolve_DenyOverrides_ShortCircuitsOtherKinds already covers this at
// the Resolve() primitive level with hand-built fixtures/lookups; this file
// covers the same scenario end-to-end through the real call path
// (Engine.EvaluateState -> the real seeded reflex_action_kinds row's
// deny_overrides classification for halt_session, migration
// 124_reflex_action_taxonomy.sql) against a real *store.Store, matching how
// this codebase's other reflex regression tests
// (dispatch_to_agent_generic_pass_test.go) are structured.
//
// This is only the same-pass preemption half of the design doc's "Halt
// must actually halt" fix (docs/engineering/architecture/
// 10-reflex-action-taxonomy.md) — the other half, turn-level synchronous
// abort of the current turn, is TASKS/reflex-taxonomy/
// 04-halt-turn-synchronicity.md's job, not this task's.

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestEvaluateState_HaltSessionPreemptsInjectReminder_SamePass(t *testing.T) {
	ctx := context.Background()
	st := newReflexTestStore(t)
	if err := st.CreateAgent(context.Background(), &store.AgentProfile{
		ID:           "agent-halt-preempt",
		Name:         "Agent Halt Preempt",
		Slug:         "agent-halt-preempt",
		Class:        "advisor",
		SystemPrompt: "test",
		Source:       "test",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	haltID, err := st.InsertAgentReflex(ctx, store.AgentReflex{
		ClassTag:    "advisor",
		Name:        "halt_probe",
		TriggerKind: store.ReflexTriggerEvent,
		TriggerSpec: `{"name":"probe"}`,
		ActionKind:  store.ReflexActionHaltSession,
		ActionSpec:  `{"reason":"test halt"}`,
		Priority:    10,
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex halt: %v", err)
	}
	reminderID, err := st.InsertAgentReflex(ctx, store.AgentReflex{
		ClassTag:    "advisor",
		Name:        "reminder_probe_unrelated",
		TriggerKind: store.ReflexTriggerEvent,
		TriggerSpec: `{"name":"probe"}`,
		ActionKind:  store.ReflexActionInjectReminder,
		// Deliberately a much higher priority than the halt reflex — proves
		// deny_overrides preemption isn't just "higher priority wins," it's
		// a different kind's action never being considered at all once a
		// deny_overrides kind has fired.
		Priority:   999,
		ActionSpec: `{"body":"unrelated reminder"}`,
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex reminder: %v", err)
	}

	hooks := &fakeReflexPluginHooks{}
	engine := NewEngine(st, nil)
	engine.SetPluginHooks(hooks)

	out, err := engine.EvaluateState(ctx, "agent-halt-preempt", "advisor", State{
		SessionID:  "sess-halt-preempt",
		AgentID:    "agent-halt-preempt",
		AgentClass: "advisor",
		Events:     []EventSignal{{EventType: "probe"}},
	})
	if err != nil {
		t.Fatalf("EvaluateState: %v", err)
	}

	if len(out.Actions) != 1 {
		t.Fatalf("Actions = %+v, want exactly 1 (the halt action only — deny_overrides must preempt the whole pass)", out.Actions)
	}
	if out.Actions[0].ActionKind != store.ReflexActionHaltSession {
		t.Fatalf("Actions[0].ActionKind = %q, want halt_session", out.Actions[0].ActionKind)
	}
	if out.FiredReflexes[0].ID != haltID {
		t.Fatalf("FiredReflexes[0].ID = %q, want the halt reflex %q", out.FiredReflexes[0].ID, haltID)
	}

	halt, err := st.GetAgentReflex(ctx, haltID)
	if err != nil {
		t.Fatalf("GetAgentReflex halt: %v", err)
	}
	if halt.FiredCount != 1 {
		t.Errorf("halt FiredCount = %d, want 1", halt.FiredCount)
	}

	reminder, err := st.GetAgentReflex(ctx, reminderID)
	if err != nil {
		t.Fatalf("GetAgentReflex reminder: %v", err)
	}
	if reminder.FiredCount != 0 {
		t.Errorf("reminder FiredCount = %d, want 0 — a preempted reflex must not be treated as having fired, despite its trigger evaluating true and its priority (999) being far higher than the halt reflex's (10)", reminder.FiredCount)
	}

	if hooks.fired != 1 || hooks.staged != 1 {
		t.Fatalf("hooks fired=%d staged=%d, want 1 each (only the halt action should reach plugin hooks)", hooks.fired, hooks.staged)
	}
}
