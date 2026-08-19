package reflexes

// TASKS/phase-4/09-fix-dispatch-to-agent-generic-pass-leak.md — coverage
// for the fix: dispatch_to_agent reflex rows must be entirely invisible
// to Engine.EvaluateState's generic per-turn pass. Before the fix,
// Executor.Apply's dispatch_to_agent case (executor.go) is a documented
// no-op that still returns (applied, nil) — EvaluateState's loop treated
// that as a real fire: it appended to Actions, bumped fired_count, and
// (when plugin hooks were wired) called EmitReflexFired/
// EmitReflexActionStaged, none of which reflects a real dispatch (the
// real dispatch only happens through the dedicated
// attemptReflexDispatch/matchDispatchToAgentReflex call sites, which
// never go through EvaluateState in the first place).

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestEvaluateState_DispatchToAgent_ExcludedFromGenericPass proves a
// firing dispatch_to_agent reflex produces NO action, NO fired_count
// bump, and NO plugin-hook emission when run through EvaluateState.
func TestEvaluateState_DispatchToAgent_ExcludedFromGenericPass(t *testing.T) {
	ctx := context.Background()
	st := newReflexTestStore(t)
	if err := st.CreateAgent(&store.AgentProfile{
		ID:           "agent-dispatch-probe",
		Name:         "Agent Dispatch Probe",
		Slug:         "agent-dispatch-probe",
		Class:        "advisor",
		SystemPrompt: "test",
		Source:       "test",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	dispatchID, err := st.InsertAgentReflex(ctx, store.AgentReflex{
		ClassTag:    "advisor",
		Name:        "dispatch_probe",
		TriggerKind: store.ReflexTriggerEvent,
		TriggerSpec: `{"name":"probe"}`,
		ActionKind:  store.ReflexActionDispatchToAgent,
		ActionSpec:  `{"agent_slug":"researcher","confidence":0.5,"reason":"test"}`,
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex: %v", err)
	}

	hooks := &fakeReflexPluginHooks{}
	engine := NewEngine(st, nil)
	engine.SetPluginHooks(hooks)

	out, err := engine.EvaluateState(ctx, "agent-dispatch-probe", "advisor", State{
		SessionID:  "sess-dispatch-probe",
		AgentID:    "agent-dispatch-probe",
		AgentClass: "advisor",
		Events:     []EventSignal{{EventType: "probe"}},
	})
	if err != nil {
		t.Fatalf("EvaluateState: %v", err)
	}

	if len(out.Actions) != 0 {
		t.Fatalf("Actions = %+v, want none — a dispatch_to_agent reflex must never surface an action from the generic pass", out.Actions)
	}
	if len(out.FiredReflexes) != 0 {
		t.Fatalf("FiredReflexes = %+v, want none", out.FiredReflexes)
	}

	reflex, err := st.GetAgentReflex(ctx, dispatchID)
	if err != nil {
		t.Fatalf("GetAgentReflex: %v", err)
	}
	if reflex.FiredCount != 0 {
		t.Errorf("FiredCount = %d, want 0 — the generic pass must not bump fired_count for a dispatch_to_agent row", reflex.FiredCount)
	}
	if reflex.LastFiredAt != "" {
		t.Errorf("LastFiredAt = %q, want empty", reflex.LastFiredAt)
	}

	if hooks.actionFilters != 0 {
		t.Errorf("actionFilters = %d, want 0 — FilterReflexAction must not run for a dispatch_to_agent row", hooks.actionFilters)
	}
	if hooks.fired != 0 {
		t.Errorf("EmitReflexFired calls = %d, want 0", hooks.fired)
	}
	if hooks.staged != 0 {
		t.Errorf("EmitReflexActionStaged calls = %d, want 0", hooks.staged)
	}
}

// TestEvaluateState_DispatchToAgent_DoesNotBlockOtherActionKinds proves
// the fix is scoped to dispatch_to_agent only: an inject_reminder reflex
// sharing the same trigger, evaluated in the same EvaluateState call,
// still fires, debounces, bumps fired_count, and emits plugin hooks
// exactly as before.
func TestEvaluateState_DispatchToAgent_DoesNotBlockOtherActionKinds(t *testing.T) {
	ctx := context.Background()
	st := newReflexTestStore(t)
	if err := st.CreateAgent(&store.AgentProfile{
		ID:           "agent-mixed-probe",
		Name:         "Agent Mixed Probe",
		Slug:         "agent-mixed-probe",
		Class:        "advisor",
		SystemPrompt: "test",
		Source:       "test",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	dispatchID, err := st.InsertAgentReflex(ctx, store.AgentReflex{
		ClassTag:    "advisor",
		Name:        "dispatch_probe_mixed",
		Priority:    20,
		TriggerKind: store.ReflexTriggerEvent,
		TriggerSpec: `{"name":"probe"}`,
		ActionKind:  store.ReflexActionDispatchToAgent,
		ActionSpec:  `{"agent_slug":"researcher","confidence":0.5,"reason":"test"}`,
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex dispatch: %v", err)
	}
	reminderID, err := st.InsertAgentReflex(ctx, store.AgentReflex{
		ClassTag:    "advisor",
		Name:        "reminder_probe_mixed",
		Priority:    10,
		TriggerKind: store.ReflexTriggerEvent,
		TriggerSpec: `{"name":"probe"}`,
		ActionKind:  store.ReflexActionInjectReminder,
		ActionSpec:  `{"body":"reminder fired"}`,
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex reminder: %v", err)
	}

	hooks := &fakeReflexPluginHooks{}
	engine := NewEngine(st, nil)
	engine.SetPluginHooks(hooks)

	out, err := engine.EvaluateState(ctx, "agent-mixed-probe", "advisor", State{
		SessionID:  "sess-mixed-probe",
		AgentID:    "agent-mixed-probe",
		AgentClass: "advisor",
		Events:     []EventSignal{{EventType: "probe"}},
	})
	if err != nil {
		t.Fatalf("EvaluateState: %v", err)
	}

	if len(out.Actions) != 1 {
		t.Fatalf("Actions = %+v, want exactly 1 (the inject_reminder reflex only)", out.Actions)
	}
	if out.Actions[0].ReflexID != reminderID {
		t.Fatalf("Actions[0].ReflexID = %q, want the reminder reflex %q", out.Actions[0].ReflexID, reminderID)
	}
	if len(out.FiredReflexes) != 1 || out.FiredReflexes[0].ID != reminderID {
		t.Fatalf("FiredReflexes = %+v, want exactly the reminder reflex", out.FiredReflexes)
	}

	reminder, err := st.GetAgentReflex(ctx, reminderID)
	if err != nil {
		t.Fatalf("GetAgentReflex reminder: %v", err)
	}
	if reminder.FiredCount != 1 {
		t.Errorf("reminder FiredCount = %d, want 1 — the 5 non-dispatch action kinds must keep bumping fired_count", reminder.FiredCount)
	}

	dispatch, err := st.GetAgentReflex(ctx, dispatchID)
	if err != nil {
		t.Fatalf("GetAgentReflex dispatch: %v", err)
	}
	if dispatch.FiredCount != 0 {
		t.Errorf("dispatch FiredCount = %d, want 0", dispatch.FiredCount)
	}

	if hooks.actionFilters != 1 || hooks.fired != 1 || hooks.staged != 1 {
		t.Fatalf("hooks action=%d fired=%d staged=%d, want 1 each (only the inject_reminder reflex should trigger plugin hooks)",
			hooks.actionFilters, hooks.fired, hooks.staged)
	}
}
