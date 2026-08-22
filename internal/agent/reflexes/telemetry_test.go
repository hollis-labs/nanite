package reflexes

// TASKS/reflex-taxonomy/06-unified-reflex-telemetry.md's Done-means:
// "One trigger of each of inject_reminder, force_tool_choice,
// halt_session, and dispatch_to_agent ... produces one consistent-shape
// trace record in the one chosen sink, with fired_count/last_fired_at
// bumped and plugin hooks fired in every case." This file covers the
// three kinds reachable through Engine.EvaluateState (the generic
// per-turn pass) — inject_reminder, force_tool_choice, halt_session.
// dispatch_to_agent's own two entry points (attemptReflexDispatch,
// internal/service/chat_reflex_dispatch_integration_test.go;
// matchDispatchToAgentReflex, internal/mcp/self_tools_dispatch_audit_test.go
// and self_tools_dispatch_cooldown_test.go) have their own equivalent
// coverage in their own packages — internal/mcp cannot be exercised from
// here (internal/mcp imports this package, not the reverse) and
// internal/service is a separate package with its own real *chatServiceImpl
// plumbing that doesn't belong duplicated in this package's tests.
//
// Also covers the "alternatives_considered-style data is now present for
// a force_tool_choice/halt_session firing" Done-means bullet —
// attemptReflexDispatch's alternatives_considered pattern was
// dispatch_to_agent-only before this task.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestEmitFirings_InjectReminder_UnifiedTraceRecordAndTelemetry(t *testing.T) {
	ctx := context.Background()
	st := newReflexTestStore(t)
	if err := st.CreateAgent(context.Background(), &store.AgentProfile{
		ID: "agent-telemetry-reminder", Name: "n", Slug: "agent-telemetry-reminder",
		Class: "advisor", SystemPrompt: "test", Source: "test",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	id, err := st.InsertAgentReflex(ctx, store.AgentReflex{
		ClassTag:    "advisor",
		Name:        "reminder_telemetry_probe",
		TriggerKind: store.ReflexTriggerEvent,
		TriggerSpec: `{"name":"probe"}`,
		ActionKind:  store.ReflexActionInjectReminder,
		ActionSpec:  `{"body":"do the thing"}`,
		CreatedBy:   "system",
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex: %v", err)
	}

	hooks := &fakeReflexPluginHooks{}
	engine := NewEngine(st, nil)
	engine.SetPluginHooks(hooks)

	const sessionID = "sess-telemetry-reminder"
	out, err := engine.EvaluateState(ctx, "agent-telemetry-reminder", "advisor", State{
		SessionID:  sessionID,
		AgentID:    "agent-telemetry-reminder",
		AgentClass: "advisor",
		Events:     []EventSignal{{EventType: "probe"}},
	})
	if err != nil {
		t.Fatalf("EvaluateState: %v", err)
	}
	if len(out.Actions) != 1 {
		t.Fatalf("Actions = %+v, want 1", out.Actions)
	}

	// fired_count/last_fired_at bump.
	reflex, err := st.GetAgentReflex(ctx, id)
	if err != nil {
		t.Fatalf("GetAgentReflex: %v", err)
	}
	if reflex.FiredCount != 1 {
		t.Errorf("FiredCount = %d, want 1", reflex.FiredCount)
	}
	if reflex.LastFiredAt == "" {
		t.Errorf("LastFiredAt = %q, want non-empty", reflex.LastFiredAt)
	}

	// Plugin hooks.
	if hooks.fired != 1 || hooks.staged != 1 {
		t.Errorf("hooks fired=%d staged=%d, want 1 each", hooks.fired, hooks.staged)
	}

	// Unified sink (event_log), one consistent trace-record shape.
	rec := findReflexTraceEvent(t, st, sessionID, store.ReflexActionInjectReminder)
	if rec["reflex_id"] != id {
		t.Errorf("metadata.reflex_id = %v, want %q", rec["reflex_id"], id)
	}
	if rec["category"] != "system_message" {
		t.Errorf("metadata.category = %v, want system_message", rec["category"])
	}
	if rec["combining_algorithm"] != "all_applicable" {
		t.Errorf("metadata.combining_algorithm = %v, want all_applicable", rec["combining_algorithm"])
	}
	if rec["provenance_tier"] != "system" {
		t.Errorf("metadata.provenance_tier = %v, want system", rec["provenance_tier"])
	}
	// all_applicable has no losers — alternatives_considered must be
	// absent, not an empty list (see EmitFirings' own doc comment).
	if _, ok := rec["alternatives_considered"]; ok {
		t.Errorf("metadata.alternatives_considered present for an all_applicable kind, want absent: %v", rec["alternatives_considered"])
	}
}

// TestEmitFirings_ForceToolChoice_AlternativesConsideredPresent is the
// Done-means bullet: "A test confirms alternatives_considered-style data
// is now present for a force_tool_choice ... firing, not just
// dispatch_to_agent." Two force_tool_choice reflexes fire; only the
// higher-priority one is selected (first_applicable) — the loser must
// show up in the winner's alternatives_considered with fired=true.
func TestEmitFirings_ForceToolChoice_AlternativesConsideredPresent(t *testing.T) {
	ctx := context.Background()
	st := newReflexTestStore(t)
	if err := st.CreateAgent(context.Background(), &store.AgentProfile{
		ID: "agent-telemetry-force", Name: "n", Slug: "agent-telemetry-force",
		Class: "advisor", SystemPrompt: "test", Source: "test",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	loID, err := st.InsertAgentReflex(ctx, store.AgentReflex{
		ClassTag: "advisor", Name: "force_low", Priority: 10,
		TriggerKind: store.ReflexTriggerEvent, TriggerSpec: `{"name":"probe"}`,
		ActionKind: store.ReflexActionForceToolChoice, ActionSpec: `{"tool_name":"tool_low"}`,
		CreatedBy: "system",
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex low: %v", err)
	}
	hiID, err := st.InsertAgentReflex(ctx, store.AgentReflex{
		ClassTag: "advisor", Name: "force_high", Priority: 90,
		TriggerKind: store.ReflexTriggerEvent, TriggerSpec: `{"name":"probe"}`,
		ActionKind: store.ReflexActionForceToolChoice, ActionSpec: `{"tool_name":"tool_high"}`,
		CreatedBy: "system",
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex high: %v", err)
	}

	engine := NewEngine(st, nil)
	const sessionID = "sess-telemetry-force"
	out, err := engine.EvaluateState(ctx, "agent-telemetry-force", "advisor", State{
		SessionID: sessionID, AgentID: "agent-telemetry-force", AgentClass: "advisor",
		Events: []EventSignal{{EventType: "probe"}},
	})
	if err != nil {
		t.Fatalf("EvaluateState: %v", err)
	}
	if len(out.Actions) != 1 || out.FiredReflexes[0].ID != hiID {
		t.Fatalf("out = %+v, want exactly the high-priority reflex selected", out)
	}
	_ = loID

	rec := findReflexTraceEvent(t, st, sessionID, store.ReflexActionForceToolChoice)
	if rec["combining_algorithm"] != "first_applicable" {
		t.Fatalf("metadata.combining_algorithm = %v, want first_applicable", rec["combining_algorithm"])
	}
	alts, ok := rec["alternatives_considered"].([]any)
	if !ok || len(alts) != 1 {
		t.Fatalf("metadata.alternatives_considered = %#v, want exactly 1 entry (the losing candidate)", rec["alternatives_considered"])
	}
	alt, ok := alts[0].(map[string]any)
	if !ok {
		t.Fatalf("alternatives_considered[0] is not an object: %#v", alts[0])
	}
	if alt["reflex_id"] != loID {
		t.Errorf("alternatives_considered[0].reflex_id = %v, want the losing reflex %q", alt["reflex_id"], loID)
	}
	if fired, _ := alt["fired"].(bool); !fired {
		t.Errorf("alternatives_considered[0].fired = %v, want true (its trigger DID fire, it just lost first_applicable's selection)", alt["fired"])
	}
}

// TestEmitFirings_HaltSession_UnifiedTraceRecordAndAlternatives covers the
// deny_overrides kind end-to-end: unified trace record shape, fired_count
// bump, plugin hooks, and alternatives_considered for the deny_overrides
// algorithm (the other Done-means-named kind besides force_tool_choice).
func TestEmitFirings_HaltSession_UnifiedTraceRecordAndAlternatives(t *testing.T) {
	ctx := context.Background()
	st := newReflexTestStore(t)
	if err := st.CreateAgent(context.Background(), &store.AgentProfile{
		ID: "agent-telemetry-halt", Name: "n", Slug: "agent-telemetry-halt",
		Class: "advisor", SystemPrompt: "test", Source: "test",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	haltID, err := st.InsertAgentReflex(ctx, store.AgentReflex{
		ClassTag: "advisor", Name: "halt_telemetry_probe", Priority: 10,
		TriggerKind: store.ReflexTriggerEvent, TriggerSpec: `{"name":"probe"}`,
		ActionKind: store.ReflexActionHaltSession, ActionSpec: `{"reason":"telemetry test"}`,
		CreatedBy: "system",
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex halt: %v", err)
	}

	hooks := &fakeReflexPluginHooks{}
	engine := NewEngine(st, nil)
	engine.SetPluginHooks(hooks)

	const sessionID = "sess-telemetry-halt"
	out, err := engine.EvaluateState(ctx, "agent-telemetry-halt", "advisor", State{
		SessionID: sessionID, AgentID: "agent-telemetry-halt", AgentClass: "advisor",
		Events: []EventSignal{{EventType: "probe"}},
	})
	if err != nil {
		t.Fatalf("EvaluateState: %v", err)
	}
	if len(out.Actions) != 1 || out.Actions[0].ActionKind != store.ReflexActionHaltSession {
		t.Fatalf("out = %+v, want exactly the halt action", out)
	}

	reflex, err := st.GetAgentReflex(ctx, haltID)
	if err != nil {
		t.Fatalf("GetAgentReflex: %v", err)
	}
	if reflex.FiredCount != 1 {
		t.Errorf("FiredCount = %d, want 1", reflex.FiredCount)
	}
	if hooks.fired != 1 || hooks.staged != 1 {
		t.Errorf("hooks fired=%d staged=%d, want 1 each", hooks.fired, hooks.staged)
	}

	rec := findReflexTraceEvent(t, st, sessionID, store.ReflexActionHaltSession)
	if rec["category"] != "execute_action" {
		t.Errorf("metadata.category = %v, want execute_action", rec["category"])
	}
	if rec["combining_algorithm"] != "deny_overrides" {
		t.Errorf("metadata.combining_algorithm = %v, want deny_overrides", rec["combining_algorithm"])
	}
	if rec["provenance_tier"] != "system" {
		t.Errorf("metadata.provenance_tier = %v, want system", rec["provenance_tier"])
	}
	spec, ok := rec["spec"].(map[string]any)
	if !ok || spec["reason"] != "telemetry test" {
		t.Errorf("metadata.spec = %#v, want spec.reason=telemetry test", rec["spec"])
	}
}

// findReflexTraceEvent locates the single event_log row this task's
// unified sink wrote for (sessionID, event_type=actionKind), fails the
// test if it's missing, and returns its decoded metadata.
func findReflexTraceEvent(t *testing.T, st *store.Store, sessionID, actionKind string) map[string]any {
	t.Helper()
	events, err := st.ListEvents(context.Background(), "reflex", 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	for _, e := range events {
		if e.SessionID != sessionID || e.EventType != actionKind {
			continue
		}
		var meta map[string]any
		if err := json.Unmarshal([]byte(e.Metadata), &meta); err != nil {
			t.Fatalf("event_log metadata not JSON: %v\nblob: %s", err, e.Metadata)
		}
		return meta
	}
	t.Fatalf("no event_log row found for session=%q event_type=%q", sessionID, actionKind)
	return nil
}
