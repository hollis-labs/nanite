package service

// TASKS/reflex-taxonomy/03-shared-decision-engine.md's Done-means
// regression test for "the force_tool_choice bug"
// (docs/engineering/architecture/10-reflex-action-taxonomy.md): two
// force_tool_choice reflexes naming different tools, both triggers true in
// the same evaluation pass, must produce exactly one action reaching
// formatReflexReminder's output — the LLM must never see two competing
// tool-choice directives in the same <system-reminder> block. Verified
// end-to-end: real *store.Store, real seeded reflex_action_kinds row
// (force_tool_choice classified first_applicable by migration
// 124_reflex_action_taxonomy.sql), real Engine.EvaluateState ->
// formatReflexReminder, the same two functions evaluateAndInjectReflexes
// chains together in production (chat_reflexes.go).

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestFormatReflexReminder_ForceToolChoiceBug_ExactlyOneActionWins is the
// regression: before this task, force_tool_choice was effectively
// all_applicable (every fired reflex landed in the same reminder block).
// After classifying it first_applicable, exactly one of the two competing
// directives below should survive to formatReflexReminder's rendered
// output — the higher-priority one.
func TestFormatReflexReminder_ForceToolChoiceBug_ExactlyOneActionWins(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if err := st.CreateAgent(context.Background(), &store.AgentProfile{
		ID:           "agent-force-tool-bug",
		Name:         "Agent Force Tool Bug",
		Slug:         "agent-force-tool-bug",
		Class:        "advisor",
		SystemPrompt: "test",
		Source:       "test",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// Two force_tool_choice reflexes naming DIFFERENT tools, same trigger,
	// different priority. Before this task's fix, both would fire and both
	// would land in the rendered reminder — a contradictory pair of
	// imperatives for the LLM.
	loID, err := st.InsertAgentReflex(ctx, store.AgentReflex{
		ClassTag:    "advisor",
		Name:        "force-tool-low-priority",
		TriggerKind: store.ReflexTriggerEvent,
		TriggerSpec: `{"name":"probe"}`,
		ActionKind:  store.ReflexActionForceToolChoice,
		Priority:    10,
		ActionSpec:  `{"tool_name":"tool_low_priority"}`,
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex low: %v", err)
	}
	hiID, err := st.InsertAgentReflex(ctx, store.AgentReflex{
		ClassTag:    "advisor",
		Name:        "force-tool-high-priority",
		TriggerKind: store.ReflexTriggerEvent,
		TriggerSpec: `{"name":"probe"}`,
		ActionKind:  store.ReflexActionForceToolChoice,
		Priority:    90,
		ActionSpec:  `{"tool_name":"tool_high_priority","enforce":true}`,
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex high: %v", err)
	}

	engine := reflexes.NewEngine(st, nil)
	out, err := engine.EvaluateState(ctx, "agent-force-tool-bug", "advisor", reflexes.State{
		SessionID:  "sess-force-tool-bug",
		AgentID:    "agent-force-tool-bug",
		AgentClass: "advisor",
		Events:     []reflexes.EventSignal{{EventType: "probe"}},
	})
	if err != nil {
		t.Fatalf("EvaluateState: %v", err)
	}
	if len(out.Actions) != 1 {
		t.Fatalf("Actions = %+v, want exactly 1 (force_tool_choice is first_applicable — only the highest-priority directive should survive)", out.Actions)
	}
	if out.FiredReflexes[0].ID != hiID {
		t.Fatalf("winner = %q, want the higher-priority reflex %q", out.FiredReflexes[0].ID, hiID)
	}
	_ = loID

	reminder := formatReflexReminder(out.Actions)
	if strings.Count(reminder, "tool_high_priority") != 1 {
		t.Errorf("expected exactly one mention of tool_high_priority in:\n%s", reminder)
	}
	if strings.Contains(reminder, "tool_low_priority") {
		t.Errorf("expected NO mention of tool_low_priority (it lost first_applicable's selection) in:\n%s", reminder)
	}
	if strings.Count(reminder, "Reflex ") != 1 {
		t.Errorf("expected exactly one 'Reflex ...' line, got:\n%s", reminder)
	}
}

// TestFormatReflexReminder_ForceToolChoiceBug_EqualPriority_EarlierCreatedAtWins
// pins the documented tie-break for the equal-priority case: the earlier
// created_at candidate wins.
func TestFormatReflexReminder_ForceToolChoiceBug_EqualPriority_EarlierCreatedAtWins(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if err := st.CreateAgent(context.Background(), &store.AgentProfile{
		ID:           "agent-force-tool-tie",
		Name:         "Agent Force Tool Tie",
		Slug:         "agent-force-tool-tie",
		Class:        "advisor",
		SystemPrompt: "test",
		Source:       "test",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	if _, err := st.InsertAgentReflex(ctx, store.AgentReflex{
		ClassTag:    "advisor",
		Name:        "force-tool-later",
		TriggerKind: store.ReflexTriggerEvent,
		TriggerSpec: `{"name":"probe"}`,
		ActionKind:  store.ReflexActionForceToolChoice,
		Priority:    50,
		CreatedAt:   "2026-01-02 00:00:00",
		ActionSpec:  `{"tool_name":"tool_later"}`,
	}); err != nil {
		t.Fatalf("InsertAgentReflex later: %v", err)
	}
	earlierID, err := st.InsertAgentReflex(ctx, store.AgentReflex{
		ClassTag:    "advisor",
		Name:        "force-tool-earlier",
		TriggerKind: store.ReflexTriggerEvent,
		TriggerSpec: `{"name":"probe"}`,
		ActionKind:  store.ReflexActionForceToolChoice,
		Priority:    50,
		CreatedAt:   "2026-01-01 00:00:00",
		ActionSpec:  `{"tool_name":"tool_earlier"}`,
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex earlier: %v", err)
	}

	engine := reflexes.NewEngine(st, nil)
	out, err := engine.EvaluateState(ctx, "agent-force-tool-tie", "advisor", reflexes.State{
		SessionID:  "sess-force-tool-tie",
		AgentID:    "agent-force-tool-tie",
		AgentClass: "advisor",
		Events:     []reflexes.EventSignal{{EventType: "probe"}},
	})
	if err != nil {
		t.Fatalf("EvaluateState: %v", err)
	}
	if len(out.Actions) != 1 {
		t.Fatalf("Actions = %+v, want exactly 1", out.Actions)
	}
	if out.FiredReflexes[0].ID != earlierID {
		t.Fatalf("winner = %q, want the earlier-created reflex %q (equal priority tie-break)", out.FiredReflexes[0].ID, earlierID)
	}

	reminder := formatReflexReminder(out.Actions)
	if !strings.Contains(reminder, "tool_earlier") {
		t.Errorf("expected tool_earlier in reminder:\n%s", reminder)
	}
	if strings.Contains(reminder, "tool_later") {
		t.Errorf("expected NO mention of tool_later:\n%s", reminder)
	}
}
