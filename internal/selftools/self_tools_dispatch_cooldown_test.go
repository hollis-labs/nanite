package selftools

// Regression coverage for TASKS/reflex-taxonomy/02-recurrence-cascade.md's
// Done-means: "setting a non-zero recurrence_override_seconds on a
// specific dispatch_to_agent reflex row suppresses re-firing within that
// window" at matchDispatchToAgentReflex specifically (the
// internal/service/chat_reflex_dispatch.go call site has its own
// equivalent regression test).

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestMatchDispatchToAgentReflex_RecurrenceOverride_SuppressesRefire
// proves the cooldown check wired into matchDispatchToAgentReflex is a
// real comparison, not a no-op: a dispatch_to_agent reflex row with a
// long recurrence_override_seconds fires on the first call (BumpAgent
// ReflexFired stamps last_fired_at), then is suppressed on an immediate
// second call against the same trigger, then would be eligible again once
// the row's last_fired_at is far enough in the past.
func TestMatchDispatchToAgentReflex_RecurrenceOverride_SuppressesRefire(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if err := s.CreateAgent(&store.AgentProfile{
		ID:           "agent-cooldown-probe",
		Name:         "Agent Cooldown Probe",
		Slug:         "agent-cooldown-probe",
		Class:        "advisor",
		SystemPrompt: "test",
		Source:       "test",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	override := int64(3600) // one hour -- long enough that any realistic
	// test runtime falls well inside it.
	reflexID, err := s.InsertAgentReflex(ctx, store.AgentReflex{
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

	st := NewSelfToolsTransport(s)

	const msg = "probe-cooldown-token please route this"

	// First call: the trigger fires, no prior last_fired_at, so nothing
	// suppresses it.
	hints1 := st.matchDispatchToAgentReflex(ctx, "sess-cooldown-1", "turn-1", "agent-cooldown-probe", msg, msg)
	if hints1 == nil {
		t.Fatal("first call: hints = nil, want a match (reflex should fire on first eligible call)")
	}
	if hints1.AgentSlug != "planner" {
		t.Errorf("first call: AgentSlug = %q, want planner", hints1.AgentSlug)
	}

	// TASKS/reflex-taxonomy/06-unified-reflex-telemetry.md gap-2
	// verification: matchDispatchToAgentReflex itself now bumps
	// fired_count/last_fired_at via reflexes.EmitFirings — this call site
	// used to be the one real gap (Engine.EvaluateState and
	// attemptReflexDispatch both bumped it; this path never did, so a
	// dispatch_to_agent reflex fired exclusively via the task_execute
	// self-tool path showed fired_count=0/last_fired_at=null in the
	// operator UI's reflex list while actively routing turns). No manual
	// bump call is needed anymore — the assertion below is the fix,
	// reproducing the exact previously-broken scenario (this call site,
	// and only this call site, having fired) and confirming it no longer
	// under-reports.
	afterFirstCall, err := s.GetAgentReflex(ctx, reflexID)
	if err != nil {
		t.Fatalf("GetAgentReflex after first call: %v", err)
	}
	if afterFirstCall.FiredCount != 1 {
		t.Fatalf("FiredCount after matchDispatchToAgentReflex alone = %d, want 1 (gap 2: this path must bump fired_count itself)", afterFirstCall.FiredCount)
	}
	if afterFirstCall.LastFiredAt == "" {
		t.Fatalf("LastFiredAt after matchDispatchToAgentReflex alone = %q, want non-empty (gap 2: this path must stamp last_fired_at itself)", afterFirstCall.LastFiredAt)
	}

	// Second call, immediately after: same trigger still fires, but the
	// 3600-second override should suppress it from winning.
	hints2 := st.matchDispatchToAgentReflex(ctx, "sess-cooldown-1", "turn-2", "agent-cooldown-probe", msg, msg)
	if hints2 != nil {
		t.Fatalf("second call (within cooldown window): hints = %+v, want nil -- recurrence_override_seconds=3600 should suppress an immediate re-fire", hints2)
	}

	// Backdate last_fired_at past the override window (well past 3600s,
	// nowhere near a real system default confusion) and confirm the
	// reflex becomes eligible again.
	row2, err := s.GetAgentReflex(ctx, reflexID)
	if err != nil {
		t.Fatalf("GetAgentReflex (re-fetch): %v", err)
	}
	row2.LastFiredAt = time.Now().Add(-2 * 3600 * time.Second).UTC().Format(time.RFC3339)
	if err := s.UpdateAgentReflex(ctx, *row2); err != nil {
		t.Fatalf("UpdateAgentReflex (backdate last_fired_at): %v", err)
	}

	hints3 := st.matchDispatchToAgentReflex(ctx, "sess-cooldown-1", "turn-3", "agent-cooldown-probe", msg, msg)
	if hints3 == nil {
		t.Fatal("third call (past cooldown window): hints = nil, want a match again")
	}
}
