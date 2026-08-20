package reflexes

// TASKS/reflex-taxonomy/02-recurrence-cascade.md's Done-means: unit tests
// for EffectiveCooldown covering all three cascade levels, including the
// explicit-zero-override case at both the kind and reflex level (a nil
// pointer is unset and falls through; a pointer to 0 is a real override
// and must NOT be treated as unset).

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

func int64p(v int64) *int64 { return &v }

func TestEffectiveCooldown_SystemDefault_WhenNeitherLevelSet(t *testing.T) {
	got := EffectiveCooldown(nil, nil)
	if got != DefaultReflexCooldown {
		t.Fatalf("EffectiveCooldown(nil, nil) = %v, want system default %v", got, DefaultReflexCooldown)
	}
}

func TestEffectiveCooldown_KindLevelOverridesSystemDefault(t *testing.T) {
	got := EffectiveCooldown(int64p(300), nil)
	want := 300 * time.Second
	if got != want {
		t.Fatalf("EffectiveCooldown(kind=300, reflex=nil) = %v, want %v", got, want)
	}
}

func TestEffectiveCooldown_KindLevelExplicitZero_MeansNoCooldown(t *testing.T) {
	got := EffectiveCooldown(int64p(0), nil)
	if got != 0 {
		t.Fatalf("EffectiveCooldown(kind=0, reflex=nil) = %v, want 0 (explicit zero is a real override, not unset)", got)
	}
}

func TestEffectiveCooldown_ReflexLevelOverridesKindAndSystem(t *testing.T) {
	got := EffectiveCooldown(int64p(300), int64p(900))
	want := 900 * time.Second
	if got != want {
		t.Fatalf("EffectiveCooldown(kind=300, reflex=900) = %v, want %v", got, want)
	}
}

func TestEffectiveCooldown_ReflexLevelExplicitZero_MeansNoCooldown(t *testing.T) {
	got := EffectiveCooldown(int64p(300), int64p(0))
	if got != 0 {
		t.Fatalf("EffectiveCooldown(kind=300, reflex=0) = %v, want 0 (explicit zero override, not unset, and it beats a nonzero kind default)", got)
	}
}

func TestEffectiveCooldown_ReflexLevelExplicitZero_OverridesUnsetKind(t *testing.T) {
	got := EffectiveCooldown(nil, int64p(0))
	if got != 0 {
		t.Fatalf("EffectiveCooldown(kind=nil, reflex=0) = %v, want 0", got)
	}
}

func TestRecentlyFired_ZeroWindowNeverSuppresses(t *testing.T) {
	now := time.Now()
	r := store.AgentReflex{LastFiredAt: now.Add(-1 * time.Second).UTC().Format(time.RFC3339)}
	if RecentlyFired(r, now, 0) {
		t.Fatal("RecentlyFired with a zero window must return false — zero cooldown means no suppression")
	}
}

func TestRecentlyFired_WithinWindowSuppresses(t *testing.T) {
	now := time.Now()
	r := store.AgentReflex{LastFiredAt: now.Add(-1 * time.Minute).UTC().Format(time.RFC3339)}
	if !RecentlyFired(r, now, 15*time.Minute) {
		t.Fatal("RecentlyFired should suppress a reflex that fired 1 minute ago against a 15-minute window")
	}
}

func TestRecentlyFired_OutsideWindowDoesNotSuppress(t *testing.T) {
	now := time.Now()
	r := store.AgentReflex{LastFiredAt: now.Add(-20 * time.Minute).UTC().Format(time.RFC3339)}
	if RecentlyFired(r, now, 15*time.Minute) {
		t.Fatal("RecentlyFired should not suppress a reflex that fired 20 minutes ago against a 15-minute window")
	}
}

// --- EvaluateState-level cascade integration tests ---
//
// These confirm the cascade is actually wired into the generic pass's
// debounce, not just unit-correct in isolation: the five non-
// dispatch_to_agent kinds still debounce at the system default (15 min)
// unchanged, and a kind- or reflex-level override actually changes the
// observed debounce window.

func fireOnceThenReEvaluate(t *testing.T, engine *Engine, agentID, agentClass string) (firstActions, secondActions int) {
	t.Helper()
	ctx := context.Background()
	state := State{AgentClass: agentClass, Events: []EventSignal{{EventType: "probe"}}}

	out1, err := engine.EvaluateState(ctx, agentID, agentClass, state)
	if err != nil {
		t.Fatalf("EvaluateState (first pass): %v", err)
	}
	out2, err := engine.EvaluateState(ctx, agentID, agentClass, state)
	if err != nil {
		t.Fatalf("EvaluateState (second pass): %v", err)
	}
	return len(out1.Actions), len(out2.Actions)
}

// TestEvaluateState_DefaultCooldown_StillDebouncesAt15Minutes confirms a
// reflex with no kind-level or reflex-level override (the state of every
// pre-existing row post-migration-124) keeps the unchanged 15-minute
// system-default debounce: firing once suppresses an immediate re-fire.
func TestEvaluateState_DefaultCooldown_StillDebouncesAt15Minutes(t *testing.T) {
	ctx := context.Background()
	st := newReflexTestStore(t)
	if err := st.CreateAgent(&store.AgentProfile{
		ID: "agent-default-cooldown", Name: "x", Slug: "agent-default-cooldown",
		Class: "process", SystemPrompt: "test", Source: "test",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if _, err := st.InsertAgentReflex(ctx, store.AgentReflex{
		ClassTag:    "process",
		Name:        "default_cooldown_probe",
		TriggerKind: store.ReflexTriggerEvent,
		TriggerSpec: `{"name":"probe"}`,
		ActionKind:  store.ReflexActionInjectReminder,
		ActionSpec:  `{"body":"fired"}`,
		// RecurrenceOverrideSeconds left nil -- inherit kind default (also
		// nil for inject_reminder) -- inherit system default.
	}); err != nil {
		t.Fatalf("InsertAgentReflex: %v", err)
	}

	engine := NewEngine(st, nil)
	first, second := fireOnceThenReEvaluate(t, engine, "agent-default-cooldown", "process")
	if first != 1 {
		t.Fatalf("first pass actions = %d, want 1 (trigger fires, no prior fired_count)", first)
	}
	if second != 0 {
		t.Fatalf("second pass actions = %d, want 0 -- system-default 15-minute cooldown should suppress an immediate re-fire", second)
	}
}

// TestEvaluateState_ReflexLevelZeroOverride_BypassesCooldown confirms an
// explicit recurrence_override_seconds=0 on a specific reflex row lets it
// re-fire on the very next pass, unlike the default-cooldown case above.
func TestEvaluateState_ReflexLevelZeroOverride_BypassesCooldown(t *testing.T) {
	ctx := context.Background()
	st := newReflexTestStore(t)
	if err := st.CreateAgent(&store.AgentProfile{
		ID: "agent-zero-override", Name: "x", Slug: "agent-zero-override",
		Class: "process", SystemPrompt: "test", Source: "test",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	zero := int64(0)
	if _, err := st.InsertAgentReflex(ctx, store.AgentReflex{
		ClassTag:                  "process",
		Name:                      "zero_override_probe",
		TriggerKind:               store.ReflexTriggerEvent,
		TriggerSpec:               `{"name":"probe"}`,
		ActionKind:                store.ReflexActionInjectReminder,
		ActionSpec:                `{"body":"fired"}`,
		RecurrenceOverrideSeconds: &zero,
	}); err != nil {
		t.Fatalf("InsertAgentReflex: %v", err)
	}

	engine := NewEngine(st, nil)
	first, second := fireOnceThenReEvaluate(t, engine, "agent-zero-override", "process")
	if first != 1 {
		t.Fatalf("first pass actions = %d, want 1", first)
	}
	if second != 1 {
		t.Fatalf("second pass actions = %d, want 1 -- recurrence_override_seconds=0 must bypass the cooldown, not be treated as unset", second)
	}
}

// TestEvaluateState_ReflexLevelPositiveOverride_ShortensOrLengthensWindow
// confirms a nonzero reflex-level override actually changes the observed
// debounce window relative to the 15-minute system default: a very short
// override (1 second) lets a reflex re-fire almost immediately once that
// window has elapsed, which the system default would still be suppressing.
func TestEvaluateState_ReflexLevelPositiveOverride_ShortensWindow(t *testing.T) {
	ctx := context.Background()
	st := newReflexTestStore(t)
	if err := st.CreateAgent(&store.AgentProfile{
		ID: "agent-short-override", Name: "x", Slug: "agent-short-override",
		Class: "process", SystemPrompt: "test", Source: "test",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	oneSecond := int64(1)
	reflexID, err := st.InsertAgentReflex(ctx, store.AgentReflex{
		ClassTag:                  "process",
		Name:                      "short_override_probe",
		TriggerKind:               store.ReflexTriggerEvent,
		TriggerSpec:               `{"name":"probe"}`,
		ActionKind:                store.ReflexActionInjectReminder,
		ActionSpec:                `{"body":"fired"}`,
		RecurrenceOverrideSeconds: &oneSecond,
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex: %v", err)
	}

	engine := NewEngine(st, nil)
	ctx2 := context.Background()
	state := State{AgentClass: "process", Events: []EventSignal{{EventType: "probe"}}}
	out1, err := engine.EvaluateState(ctx2, "agent-short-override", "process", state)
	if err != nil {
		t.Fatalf("EvaluateState (first pass): %v", err)
	}
	if len(out1.Actions) != 1 {
		t.Fatalf("first pass actions = %d, want 1", len(out1.Actions))
	}

	// Immediately re-evaluating (well within 1 second) must still suppress.
	outImmediate, err := engine.EvaluateState(ctx2, "agent-short-override", "process", state)
	if err != nil {
		t.Fatalf("EvaluateState (immediate second pass): %v", err)
	}
	if len(outImmediate.Actions) != 0 {
		t.Fatalf("immediate second pass actions = %d, want 0 -- a 1-second override should still suppress an immediate re-fire", len(outImmediate.Actions))
	}

	// Backdate last_fired_at by 2 seconds -- past the 1-second override
	// window but nowhere near the 15-minute system default, proving the
	// override (not the default) is what's actually governing eligibility.
	// Fetch-then-update so every other column (priority, fired_count, etc.)
	// round-trips unchanged rather than getting clobbered by Go zero values.
	row, err := st.GetAgentReflex(ctx, reflexID)
	if err != nil {
		t.Fatalf("GetAgentReflex: %v", err)
	}
	row.LastFiredAt = time.Now().Add(-2 * time.Second).UTC().Format(time.RFC3339)
	if err := st.UpdateAgentReflex(ctx, *row); err != nil {
		t.Fatalf("UpdateAgentReflex: %v", err)
	}

	outLater, err := engine.EvaluateState(ctx2, "agent-short-override", "process", state)
	if err != nil {
		t.Fatalf("EvaluateState (later pass): %v", err)
	}
	if len(outLater.Actions) != 1 {
		t.Fatalf("later pass actions = %d, want 1 -- past the 1-second override window, the reflex should be eligible again well before the 15-minute system default would allow", len(outLater.Actions))
	}
}

// TestEvaluateState_KindLevelOverride_ChangesDebounceWindow confirms the
// kind-level tier of the cascade (reflex_action_kinds.
// default_recurrence_seconds) is actually consulted: dispatch_to_agent's
// seeded kind-level override is 0 ("no cooldown"). Although
// dispatch_to_agent rows are excluded from EvaluateState's generic pass
// entirely (Phase 4 item 09), ActionKindDefaultRecurrenceSeconds itself is
// exercised directly here against the live seeded row to prove the cache
// loaded real data, not just a fixture value.
func TestEngine_ActionKindDefaultRecurrenceSeconds_LoadsSeededDispatchToAgentZero(t *testing.T) {
	st := newReflexTestStore(t)
	engine := NewEngine(st, nil)

	got := engine.ActionKindDefaultRecurrenceSeconds(store.ReflexActionDispatchToAgent)
	if got == nil || *got != 0 {
		t.Fatalf("ActionKindDefaultRecurrenceSeconds(dispatch_to_agent) = %v, want a pointer to 0 (migration 124's seeded override)", got)
	}

	// A non-dispatch kind (e.g. inject_reminder) is seeded with a NULL
	// default_recurrence_seconds -- nil, meaning "inherit the system
	// default" -- not zero.
	gotReminder := engine.ActionKindDefaultRecurrenceSeconds(store.ReflexActionInjectReminder)
	if gotReminder != nil {
		t.Fatalf("ActionKindDefaultRecurrenceSeconds(inject_reminder) = %v, want nil (seeded NULL, inherit system default)", gotReminder)
	}
}

// TestEngine_RefreshActionKindCache_PicksUpChange proves the manual
// refresh hook actually re-reads the store rather than being a no-op.
func TestEngine_RefreshActionKindCache_PicksUpChange(t *testing.T) {
	ctx := context.Background()
	st := newReflexTestStore(t)
	engine := NewEngine(st, nil)

	before := engine.ActionKindDefaultRecurrenceSeconds(store.ReflexActionInjectReminder)
	if before != nil {
		t.Fatalf("before = %v, want nil", before)
	}

	if _, err := st.DB.ExecContext(ctx,
		`UPDATE reflex_action_kinds SET default_recurrence_seconds = 60 WHERE name = ?`,
		store.ReflexActionInjectReminder,
	); err != nil {
		t.Fatalf("update reflex_action_kinds: %v", err)
	}

	// Cache is stale until RefreshActionKindCache is called.
	stale := engine.ActionKindDefaultRecurrenceSeconds(store.ReflexActionInjectReminder)
	if stale != nil {
		t.Fatalf("stale = %v, want nil (cache not yet refreshed)", stale)
	}

	if err := engine.RefreshActionKindCache(ctx); err != nil {
		t.Fatalf("RefreshActionKindCache: %v", err)
	}

	after := engine.ActionKindDefaultRecurrenceSeconds(store.ReflexActionInjectReminder)
	if after == nil || *after != 60 {
		t.Fatalf("after refresh = %v, want a pointer to 60", after)
	}
}
