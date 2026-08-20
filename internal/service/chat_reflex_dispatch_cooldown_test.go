package service

// Regression coverage for TASKS/reflex-taxonomy/02-recurrence-cascade.md's
// Done-means: "setting a non-zero recurrence_override_seconds on a
// specific dispatch_to_agent reflex row suppresses re-firing within that
// window" at attemptReflexDispatch specifically (internal/mcp/
// self_tools_dispatch.go's matchDispatchToAgentReflex has its own
// equivalent regression test, proving the two independent call sites
// both apply the shared reflexes.EffectiveCooldown/RecentlyFired check).

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestAttemptReflexDispatch_RecurrenceOverride_SuppressesRefire proves
// attemptReflexDispatch's cooldown check is a real comparison: a
// dispatch_to_agent reflex row with a long recurrence_override_seconds
// fires on the first turn, is suppressed on an immediate second turn
// against the same trigger, then fires again once its last_fired_at is
// backdated past the override window.
func TestAttemptReflexDispatch_RecurrenceOverride_SuppressesRefire(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reflex-dispatch-cooldown.db")
	st, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	override := int64(3600) // one hour -- long enough that any realistic
	// test runtime falls well inside it.
	reflexID, err := st.InsertAgentReflex(context.Background(), store.AgentReflex{
		ClassTag:                  "advisor",
		Name:                      "dispatch_cooldown_probe",
		TriggerKind:               store.ReflexTriggerPredicate,
		TriggerSpec:               `{"kind":"user_regex_window","window":1,"pattern":"probe-cooldown-token"}`,
		ActionKind:                store.ReflexActionDispatchToAgent,
		ActionSpec:                `{"agent_slug":"planner","confidence":0.9,"reason":"cooldown test"}`,
		Priority:                  50,
		RecurrenceOverrideSeconds: &override,
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex: %v", err)
	}

	tools := &recordingReflexDispatchToolService{}
	s := &chatServiceImpl{
		store:        st,
		reflexEngine: reflexes.NewEngine(st, nil),
		tools:        tools,
	}

	const userMessage = "probe-cooldown-token please route this"
	newTurn := func(sessionID string) *loopState {
		ls := newLoopState(chat.AgentConstraints{}, nil, false)
		classifyAndAttach(ls, sessionID, userMessage, nil)
		return ls
	}

	ch := make(chan chat.StreamEvent, 4)
	out1 := s.attemptReflexDispatch(
		context.Background(),
		"sess-cooldown-1", "turn-1", userMessage,
		"agent-cooldown-1", "advisor",
		newTurn("sess-cooldown-1"), ch,
	)
	close(ch)
	if !out1.Matched {
		t.Fatalf("first turn: Matched = false, want true (reflex should fire on first eligible turn)")
	}
	if out1.AgentSlug != "planner" {
		t.Errorf("first turn: AgentSlug = %q, want planner", out1.AgentSlug)
	}

	// Immediately re-run the same turn shape. The reflex's own trigger
	// still fires (same message), but the 3600-second override — bumped
	// by the first call above — should now suppress it.
	ch2 := make(chan chat.StreamEvent, 4)
	out2 := s.attemptReflexDispatch(
		context.Background(),
		"sess-cooldown-1", "turn-2", userMessage,
		"agent-cooldown-1", "advisor",
		newTurn("sess-cooldown-1"), ch2,
	)
	close(ch2)
	if out2.Matched {
		t.Fatalf("second turn (within cooldown window): Matched = true, want false -- recurrence_override_seconds=3600 should suppress an immediate re-fire")
	}
	if len(tools.calls) != 1 {
		t.Fatalf("ToolService.Execute called %d times after 2 turns, want 1 (second turn suppressed by cooldown)", len(tools.calls))
	}

	// Backdate last_fired_at past the override window and confirm the
	// reflex becomes eligible again.
	row, err := st.GetAgentReflex(context.Background(), reflexID)
	if err != nil {
		t.Fatalf("GetAgentReflex: %v", err)
	}
	row.LastFiredAt = time.Now().Add(-2 * 3600 * time.Second).UTC().Format(time.RFC3339)
	if err := st.UpdateAgentReflex(context.Background(), *row); err != nil {
		t.Fatalf("UpdateAgentReflex (backdate last_fired_at): %v", err)
	}

	ch3 := make(chan chat.StreamEvent, 4)
	out3 := s.attemptReflexDispatch(
		context.Background(),
		"sess-cooldown-1", "turn-3", userMessage,
		"agent-cooldown-1", "advisor",
		newTurn("sess-cooldown-1"), ch3,
	)
	close(ch3)
	if !out3.Matched {
		t.Fatalf("third turn (past cooldown window): Matched = false, want true again")
	}
	if len(tools.calls) != 2 {
		t.Fatalf("ToolService.Execute called %d times after 3 turns, want 2 (turn 1 and turn 3 should both dispatch)", len(tools.calls))
	}
}
