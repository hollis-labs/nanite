package reflexes

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// These are the Done-means tests for
// TASKS/phase-1/07-add-reflex-opt-out-field.md: a required
// (opt_out_allowed=false) reflex still fires for an agent that has
// attempted to opt out of it via agent_reflex_opt_outs, while a
// default-on (opt_out_allowed=true) reflex is genuinely suppressed by the
// same mechanism. Each case below uses its own freshly-inserted reflex
// row rather than sharing one across assertions — Engine.EvaluateState's
// recentlyFired 15-minute re-fire guard (engine.go) means a row that has
// already fired once won't fire again on a second EvaluateState call
// moments later regardless of the opt-out logic, so reusing rows across
// "before" and "after" assertions would conflate that guard with the
// behavior under test.

func newOptOutTestAgent(t *testing.T, st *store.Store, id string) {
	t.Helper()
	if err := st.CreateAgent(&store.AgentProfile{
		ID: id, Name: id, Slug: id, Class: "process",
		SystemPrompt: "test", Source: "test",
	}); err != nil {
		t.Fatalf("CreateAgent %s: %v", id, err)
	}
}

// TestReflexOptOut_RequiredFiresDespiteOptOutAttempt: opt_out_allowed=false
// means the reflex applies unconditionally, even when an
// agent_reflex_opt_outs row exists for it.
func TestReflexOptOut_RequiredFiresDespiteOptOutAttempt(t *testing.T) {
	ctx := context.Background()
	st := newReflexTestStore(t)
	newOptOutTestAgent(t, st, "agent-required-probe")

	requiredID, err := st.InsertAgentReflex(ctx, store.AgentReflex{
		ClassTag:      "process",
		Name:          "required_probe",
		TriggerKind:   store.ReflexTriggerEvent,
		TriggerSpec:   `{"name":"probe"}`,
		ActionKind:    store.ReflexActionInjectReminder,
		ActionSpec:    `{"body":"required fired"}`,
		OptOutAllowed: false,
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex: %v", err)
	}

	// The agent attempts to opt out before the reflex has ever evaluated.
	if err := st.SetAgentReflexOptOut(ctx, "agent-required-probe", requiredID); err != nil {
		t.Fatalf("SetAgentReflexOptOut: %v", err)
	}

	engine := NewEngine(st, nil)
	out, err := engine.EvaluateState(ctx, "agent-required-probe", "process", State{
		AgentClass: "process",
		Events:     []EventSignal{{EventType: "probe"}},
	})
	if err != nil {
		t.Fatalf("EvaluateState: %v", err)
	}
	if len(out.Actions) != 1 || out.Actions[0].ReflexID != requiredID {
		t.Fatalf("actions = %+v, want exactly the required reflex (%s) to fire despite the opt-out attempt", out.Actions, requiredID)
	}
}

// TestReflexOptOut_PermissiveCanBeSuppressedPerAgent: opt_out_allowed=true
// (the default) means an agent_reflex_opt_outs row genuinely suppresses
// the class-bound reflex for that agent.
func TestReflexOptOut_PermissiveCanBeSuppressedPerAgent(t *testing.T) {
	ctx := context.Background()
	st := newReflexTestStore(t)
	newOptOutTestAgent(t, st, "agent-permissive-probe")

	permissiveID, err := st.InsertAgentReflex(ctx, store.AgentReflex{
		ClassTag:      "process",
		Name:          "permissive_probe",
		TriggerKind:   store.ReflexTriggerEvent,
		TriggerSpec:   `{"name":"probe"}`,
		ActionKind:    store.ReflexActionInjectReminder,
		ActionSpec:    `{"body":"permissive fired"}`,
		OptOutAllowed: true,
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex: %v", err)
	}
	if err := st.SetAgentReflexOptOut(ctx, "agent-permissive-probe", permissiveID); err != nil {
		t.Fatalf("SetAgentReflexOptOut: %v", err)
	}

	engine := NewEngine(st, nil)
	out, err := engine.EvaluateState(ctx, "agent-permissive-probe", "process", State{
		AgentClass: "process",
		Events:     []EventSignal{{EventType: "probe"}},
	})
	if err != nil {
		t.Fatalf("EvaluateState: %v", err)
	}
	if len(out.Actions) != 0 {
		t.Fatalf("actions = %+v, want none — opt_out_allowed=true reflex should be suppressed by the opt-out row", out.Actions)
	}
}

// TestReflexOptOut_ClearingOptOutReenables: ClearAgentReflexOptOut
// reverses SetAgentReflexOptOut — a previously-suppressed permissive
// reflex fires again once the opt-out marker is removed.
func TestReflexOptOut_ClearingOptOutReenables(t *testing.T) {
	ctx := context.Background()
	st := newReflexTestStore(t)
	newOptOutTestAgent(t, st, "agent-clear-probe")

	permissiveID, err := st.InsertAgentReflex(ctx, store.AgentReflex{
		ClassTag:      "process",
		Name:          "permissive_probe",
		TriggerKind:   store.ReflexTriggerEvent,
		TriggerSpec:   `{"name":"probe"}`,
		ActionKind:    store.ReflexActionInjectReminder,
		ActionSpec:    `{"body":"permissive fired"}`,
		OptOutAllowed: true,
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex: %v", err)
	}
	if err := st.SetAgentReflexOptOut(ctx, "agent-clear-probe", permissiveID); err != nil {
		t.Fatalf("SetAgentReflexOptOut: %v", err)
	}
	if err := st.ClearAgentReflexOptOut(ctx, "agent-clear-probe", permissiveID); err != nil {
		t.Fatalf("ClearAgentReflexOptOut: %v", err)
	}

	engine := NewEngine(st, nil)
	out, err := engine.EvaluateState(ctx, "agent-clear-probe", "process", State{
		AgentClass: "process",
		Events:     []EventSignal{{EventType: "probe"}},
	})
	if err != nil {
		t.Fatalf("EvaluateState: %v", err)
	}
	if len(out.Actions) != 1 || out.Actions[0].ReflexID != permissiveID {
		t.Fatalf("actions = %+v, want the reflex to fire again once its opt-out was cleared", out.Actions)
	}
}

// TestReflexOptOut_OnlyAppliesToTheOptingOutAgent verifies the opt-out
// row is scoped per-agent: a second agent of the same class that has not
// opted out still gets the class-bound reflex.
func TestReflexOptOut_OnlyAppliesToTheOptingOutAgent(t *testing.T) {
	ctx := context.Background()
	st := newReflexTestStore(t)
	newOptOutTestAgent(t, st, "agent-a")
	newOptOutTestAgent(t, st, "agent-b")

	permissiveID, err := st.InsertAgentReflex(ctx, store.AgentReflex{
		ClassTag:      "process",
		Name:          "permissive_probe",
		TriggerKind:   store.ReflexTriggerEvent,
		TriggerSpec:   `{"name":"probe"}`,
		ActionKind:    store.ReflexActionInjectReminder,
		ActionSpec:    `{"body":"permissive fired"}`,
		OptOutAllowed: true,
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex: %v", err)
	}
	if err := st.SetAgentReflexOptOut(ctx, "agent-a", permissiveID); err != nil {
		t.Fatalf("SetAgentReflexOptOut: %v", err)
	}

	engine := NewEngine(st, nil)
	state := State{AgentClass: "process", Events: []EventSignal{{EventType: "probe"}}}

	outA, err := engine.EvaluateState(ctx, "agent-a", "process", state)
	if err != nil {
		t.Fatalf("EvaluateState agent-a: %v", err)
	}
	if len(outA.Actions) != 0 {
		t.Fatalf("agent-a (opted out) actions = %d, want 0", len(outA.Actions))
	}

	outB, err := engine.EvaluateState(ctx, "agent-b", "process", state)
	if err != nil {
		t.Fatalf("EvaluateState agent-b: %v", err)
	}
	if len(outB.Actions) != 1 {
		t.Fatalf("agent-b (not opted out) actions = %d, want 1", len(outB.Actions))
	}
}
