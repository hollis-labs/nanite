package contextbroker

import (
	"context"
	"strings"
	"testing"

	ctxpkg "github.com/hollis-labs/nanite/internal/context"
)

// fakeStasher is a deterministic in-memory SlotStasher for the
// assembly_test.go suite. It mirrors the contract the production
// internal/service implementation honors: content-addressed IDs,
// idempotent re-stash, no error path under normal conditions. Tests
// that want to exercise the stash-failure fallback use
// failingStasher below.
type fakeStasher struct {
	stored map[string]string // artifact_id → content
}

func newFakeStasher() *fakeStasher {
	return &fakeStasher{stored: map[string]string{}}
}

func (f *fakeStasher) StashSlot(_ context.Context, req StashRequest) (StashResult, error) {
	id := DeterministicArtifactID(req.SessionID, req.SlotName, req.Content)
	_, reused := f.stored[id]
	f.stored[id] = req.Content
	return StashResult{ArtifactID: id, Reused: reused}, nil
}

// failingStasher always returns ErrStashUnavailable so tests can exercise
// the broker's atomic fallback path (oversized slot → ActionShip when
// stash fails) without rigging real I/O failure.
type failingStasher struct{}

func (failingStasher) StashSlot(_ context.Context, _ StashRequest) (StashResult, error) {
	return StashResult{}, ErrStashUnavailable
}

// minimalBudgets returns the canonical default budget map for tests. Kept as a
// helper so a future SlotOrder addition doesn't require updating every test.
func minimalBudgets() map[string]int {
	return ctxpkg.DefaultBudgets()
}

func TestDecideAssembly_UniversalSlotAtPosition0(t *testing.T) {
	// The Universal slot must be position 0 in the plan's decisions
	// regardless of whether it has content. SP-20260512-0008 W1A
	// (CW-20260512-0104) acceptance: universal slot always present at
	// position 0.
	plan := DecideAssembly(context.Background(), AssemblyInput{
		Intent:    Intent{Type: IntentCustom},
		SlotOrder: ctxpkg.SlotOrder,
		Sources: map[string]string{
			ctxpkg.SlotUniversal: "", // empty — Sprint 2 wires content
			ctxpkg.SlotSystem:    "system content",
			ctxpkg.SlotAgent:     "agent content",
		},
		Budgets: minimalBudgets(),
		Stasher: newFakeStasher(),
	})

	if len(plan.Decisions) != len(ctxpkg.SlotOrder) {
		t.Fatalf("expected %d decisions (one per slot), got %d",
			len(ctxpkg.SlotOrder), len(plan.Decisions))
	}
	if plan.Decisions[0].SlotName != ctxpkg.SlotUniversal {
		t.Errorf("position 0 must be SlotUniversal, got %q", plan.Decisions[0].SlotName)
	}
	if plan.Decisions[0].Action != ActionSkip || plan.Decisions[0].ReasonTag != "skipped_no_content" {
		t.Errorf("empty universal slot should skip with reason=skipped_no_content, got action=%v reason=%q",
			plan.Decisions[0].Action, plan.Decisions[0].ReasonTag)
	}
}

func TestDecideAssembly_UniversalSlotPositionStableAcrossTurns(t *testing.T) {
	// The cacheable prefix relies on slot POSITIONS being stable across
	// turns even when CONTENT changes. Walk three turn variants — empty,
	// populated, oversized — and confirm SlotUniversal is at position 0
	// every time.
	for _, content := range []string{"", "## Universal rules\n- be honest", strings.Repeat("x", 5000)} {
		plan := DecideAssembly(context.Background(), AssemblyInput{
			Intent:    Intent{Type: IntentCustom},
			SlotOrder: ctxpkg.SlotOrder,
			Sources: map[string]string{
				ctxpkg.SlotUniversal: content,
				ctxpkg.SlotSystem:    "stable system",
			},
			Budgets: minimalBudgets(),
			Stasher: newFakeStasher(),
		})
		if plan.Decisions[0].SlotName != ctxpkg.SlotUniversal {
			t.Errorf("content=%q: position 0 must be SlotUniversal, got %q", content, plan.Decisions[0].SlotName)
		}
	}
}

func TestDecideAssembly_SkipsContextSlotForReviewIntent(t *testing.T) {
	// Ticket acceptance criterion: a turn that doesn't need workspace
	// context doesn't ship the workspace slot. Review/recall/resume turns
	// pull their grounding from session+memory, not from the workspace
	// context slot — the broker should skip SlotContext when the intent
	// is one of those.
	cases := []struct {
		intentType string
		wantSkip   bool
	}{
		{IntentReviewSession, true},
		{IntentRecallDecision, true},
		{IntentResumeTask, true},
		{IntentWriteCode, false},
		{IntentDebugIssue, false},
		{IntentBootProject, false},
		{IntentPlanFeature, false},
		{IntentCustom, false},
	}

	for _, tc := range cases {
		plan := DecideAssembly(context.Background(), AssemblyInput{
			Intent:    Intent{Type: tc.intentType},
			SlotOrder: ctxpkg.SlotOrder,
			Sources: map[string]string{
				ctxpkg.SlotContext: "workspace context content",
				ctxpkg.SlotSystem:  "always ships",
			},
			Budgets: minimalBudgets(),
			Stasher: newFakeStasher(),
		})

		var ctxDecision *SlotDecision
		for i := range plan.Decisions {
			if plan.Decisions[i].SlotName == ctxpkg.SlotContext {
				ctxDecision = &plan.Decisions[i]
				break
			}
		}
		if ctxDecision == nil {
			t.Fatalf("intent=%s: SlotContext decision missing from plan", tc.intentType)
		}

		if tc.wantSkip {
			if ctxDecision.Action != ActionSkip {
				t.Errorf("intent=%s: expected SlotContext ActionSkip, got %v", tc.intentType, ctxDecision.Action)
			}
			if ctxDecision.ReasonTag != "skipped_no_intent_match" {
				t.Errorf("intent=%s: expected reason=skipped_no_intent_match, got %q", tc.intentType, ctxDecision.ReasonTag)
			}
			if got := plan.Stash[ctxpkg.SlotContext]; got != "workspace context content" {
				t.Errorf("intent=%s: skipped content should be in stash, got %q", tc.intentType, got)
			}
		} else {
			if ctxDecision.Action != ActionShip {
				t.Errorf("intent=%s: expected SlotContext ActionShip, got %v", tc.intentType, ctxDecision.Action)
			}
		}
	}
}

func TestDecideAssembly_PointerForOversizedSlot(t *testing.T) {
	// Content over the per-slot budget gets stashed to the artifact store
	// and substituted with a pointer envelope referencing the artifact_id.
	// The pointer content matches the documented format
	// `<ref:artifact_id=ART-..., tokens=N, available via dev_read>`.
	// SP-20260512-0008 W2C (CW-20260512-0110).
	budgets := minimalBudgets()
	budgets[ctxpkg.SlotMemory] = 10 // tight cap

	bigContent := strings.Repeat("memory body ", 100) // ~1200 chars = 300 tokens

	plan := DecideAssembly(context.Background(), AssemblyInput{
		Intent:    Intent{Type: IntentCustom},
		SlotOrder: ctxpkg.SlotOrder,
		SessionID: "test-session",
		Sources: map[string]string{
			ctxpkg.SlotMemory: bigContent,
		},
		Budgets: budgets,
		Stasher: newFakeStasher(),
	})

	var memDecision *SlotDecision
	for i := range plan.Decisions {
		if plan.Decisions[i].SlotName == ctxpkg.SlotMemory {
			memDecision = &plan.Decisions[i]
			break
		}
	}
	if memDecision == nil {
		t.Fatal("memory decision missing")
	}
	if memDecision.Action != ActionPointer {
		t.Errorf("expected ActionPointer for oversized slot, got %v", memDecision.Action)
	}
	if !strings.HasPrefix(memDecision.Content, "<ref:artifact_id=art-stash-") {
		t.Errorf("pointer content malformed: %q", memDecision.Content)
	}
	if !strings.Contains(memDecision.Content, "available via dev_read>") {
		t.Errorf("pointer content missing dev_read affordance: %q", memDecision.Content)
	}
	if memDecision.ArtifactID == "" {
		t.Errorf("pointer decision should carry ArtifactID")
	}
	if !strings.Contains(memDecision.Content, memDecision.ArtifactID) {
		t.Errorf("pointer content %q should embed ArtifactID %q", memDecision.Content, memDecision.ArtifactID)
	}
	if plan.Stash[ctxpkg.SlotMemory] != bigContent {
		t.Errorf("original content should be stashed for oversized slot")
	}
}

func TestDecideAssembly_PointerArtifactIDStableAcrossTurns(t *testing.T) {
	// Cache-implications sharp edge: pointer envelopes for stable content
	// (e.g. AGENTS.md walk-up) must be byte-identical across turns so the
	// cacheable prefix is preserved. Same (session, slot, content) →
	// same artifact_id → same pointer envelope.
	budgets := minimalBudgets()
	budgets[ctxpkg.SlotMemory] = 10

	bigContent := strings.Repeat("stable memory ", 200)
	stasher := newFakeStasher()

	plans := make([]AssemblyPlan, 0, 3)
	for i := 0; i < 3; i++ {
		plans = append(plans, DecideAssembly(context.Background(), AssemblyInput{
			Intent:    Intent{Type: IntentCustom},
			SlotOrder: ctxpkg.SlotOrder,
			SessionID: "stable-session",
			Sources:   map[string]string{ctxpkg.SlotMemory: bigContent},
			Budgets:   budgets,
			Stasher:   stasher,
		}))
	}

	first := pointerFor(plans[0], ctxpkg.SlotMemory)
	if first == "" {
		t.Fatal("first turn missing memory pointer")
	}
	for i, p := range plans {
		got := pointerFor(p, ctxpkg.SlotMemory)
		if got != first {
			t.Errorf("turn %d pointer drift: got %q, want %q", i, got, first)
		}
	}
}

func TestDecideAssembly_PointerArtifactIDChangesWithContent(t *testing.T) {
	// Content-addressed ID: when the slot's content changes, the
	// artifact_id changes too — different content → different pointer
	// envelope → cache-prefix is correctly invalidated.
	budgets := minimalBudgets()
	budgets[ctxpkg.SlotMemory] = 10
	stasher := newFakeStasher()

	planA := DecideAssembly(context.Background(), AssemblyInput{
		Intent:    Intent{Type: IntentCustom},
		SlotOrder: ctxpkg.SlotOrder,
		SessionID: "session-x",
		Sources:   map[string]string{ctxpkg.SlotMemory: strings.Repeat("A ", 500)},
		Budgets:   budgets,
		Stasher:   stasher,
	})
	planB := DecideAssembly(context.Background(), AssemblyInput{
		Intent:    Intent{Type: IntentCustom},
		SlotOrder: ctxpkg.SlotOrder,
		SessionID: "session-x",
		Sources:   map[string]string{ctxpkg.SlotMemory: strings.Repeat("B ", 500)},
		Budgets:   budgets,
		Stasher:   stasher,
	})

	a := pointerFor(planA, ctxpkg.SlotMemory)
	b := pointerFor(planB, ctxpkg.SlotMemory)
	if a == b {
		t.Errorf("pointer envelope should differ when content differs; both = %q", a)
	}
}

func TestDecideAssembly_PointerFallbackWhenStashFails(t *testing.T) {
	// Atomicity contract (★ load-bearing): if the stash write fails, the
	// pointer is NOT emitted. The decider falls back to ActionShip with
	// the full content and ReasonTag="pointer_fallback_ship". The wire
	// must never reference a non-existent artifact.
	budgets := minimalBudgets()
	budgets[ctxpkg.SlotMemory] = 10
	bigContent := strings.Repeat("memory body ", 100)

	plan := DecideAssembly(context.Background(), AssemblyInput{
		Intent:    Intent{Type: IntentCustom},
		SlotOrder: ctxpkg.SlotOrder,
		SessionID: "test-session",
		Sources:   map[string]string{ctxpkg.SlotMemory: bigContent},
		Budgets:   budgets,
		Stasher:   failingStasher{},
	})

	memDec := decisionFor(plan, ctxpkg.SlotMemory)
	if memDec == nil {
		t.Fatal("memory decision missing")
	}
	if memDec.Action != ActionShip {
		t.Errorf("expected ActionShip on stash failure, got %v", memDec.Action)
	}
	if memDec.ReasonTag != "pointer_fallback_ship" {
		t.Errorf("expected reason=pointer_fallback_ship, got %q", memDec.ReasonTag)
	}
	if memDec.Content != bigContent {
		t.Errorf("fallback should ship full content; got %d bytes, want %d", len(memDec.Content), len(bigContent))
	}
	if memDec.ArtifactID != "" {
		t.Errorf("fallback must not carry ArtifactID (it would lie about a stashed artifact); got %q", memDec.ArtifactID)
	}
	if plan.Stash[ctxpkg.SlotMemory] != bigContent {
		t.Errorf("fallback should still record content in in-memory stash for callers")
	}
}

func TestDecideAssembly_NoStasherFallsBackToInlineShip(t *testing.T) {
	// When AssemblyInput.Stasher is nil, the decider uses NopStasher()
	// which returns ErrStashUnavailable. Oversized slots ship inline
	// rather than emit a pointer to nowhere — same fallback path as
	// TestDecideAssembly_PointerFallbackWhenStashFails. Verifies the
	// nil-stasher convenience path explicitly.
	budgets := minimalBudgets()
	budgets[ctxpkg.SlotMemory] = 10
	bigContent := strings.Repeat("x ", 500)

	plan := DecideAssembly(context.Background(), AssemblyInput{
		Intent:    Intent{Type: IntentCustom},
		SlotOrder: ctxpkg.SlotOrder,
		Sources:   map[string]string{ctxpkg.SlotMemory: bigContent},
		Budgets:   budgets,
		// Stasher: nil — exercise the nop-default path.
	})
	memDec := decisionFor(plan, ctxpkg.SlotMemory)
	if memDec == nil || memDec.Action != ActionShip {
		t.Fatalf("expected ActionShip with nil stasher, got %+v", memDec)
	}
}

// pointerFor returns the rendered pointer string for a slot in a plan, or
// empty if the slot's decision isn't an ActionPointer.
func pointerFor(plan AssemblyPlan, slotName string) string {
	for _, d := range plan.Decisions {
		if d.SlotName == slotName && d.Action == ActionPointer {
			return d.Content
		}
	}
	return ""
}

// decisionFor returns the decision for a named slot, or nil if absent.
func decisionFor(plan AssemblyPlan, slotName string) *SlotDecision {
	for i := range plan.Decisions {
		if plan.Decisions[i].SlotName == slotName {
			return &plan.Decisions[i]
		}
	}
	return nil
}

func TestDecideAssembly_EmptyContentSkippedWithoutStash(t *testing.T) {
	// Empty content emits ActionSkip with reason=skipped_no_content and
	// does NOT create a stash entry — there's nothing to stash. Future
	// callers that probe the stash to recover content can rely on
	// "stash key present" as the signal that real content was deferred.
	plan := DecideAssembly(context.Background(), AssemblyInput{
		Intent:    Intent{Type: IntentCustom},
		SlotOrder: ctxpkg.SlotOrder,
		Sources: map[string]string{
			ctxpkg.SlotMemory: "",
			ctxpkg.SlotSystem: "real content",
		},
		Budgets: minimalBudgets(),
		Stasher: newFakeStasher(),
	})

	var memDec *SlotDecision
	for i := range plan.Decisions {
		if plan.Decisions[i].SlotName == ctxpkg.SlotMemory {
			memDec = &plan.Decisions[i]
		}
	}
	if memDec == nil {
		t.Fatal("memory decision missing")
	}
	if memDec.Action != ActionSkip || memDec.ReasonTag != "skipped_no_content" {
		t.Errorf("empty content: want Skip/skipped_no_content, got action=%v reason=%q",
			memDec.Action, memDec.ReasonTag)
	}
	if _, present := plan.Stash[ctxpkg.SlotMemory]; present {
		t.Errorf("empty-content skip should not populate stash; got entry %q",
			plan.Stash[ctxpkg.SlotMemory])
	}
}

func TestDecideAssembly_OrderMatchesSlotOrder(t *testing.T) {
	// Plan decisions must be in SlotOrder exactly — downstream cache
	// markers depend on the position being stable. Mix populated /
	// empty / oversized across several slots and verify the decision
	// list walks SlotOrder verbatim.
	budgets := minimalBudgets()
	budgets[ctxpkg.SlotMemory] = 5 // force pointer

	plan := DecideAssembly(context.Background(), AssemblyInput{
		Intent:    Intent{Type: IntentWriteCode},
		SlotOrder: ctxpkg.SlotOrder,
		Sources: map[string]string{
			ctxpkg.SlotUniversal:   "",
			ctxpkg.SlotSystem:      "system",
			ctxpkg.SlotMemory:      strings.Repeat("mem ", 200),
			ctxpkg.SlotAgent:       "agent",
			ctxpkg.SlotMode:        "",
			ctxpkg.SlotRules:       "rules",
			ctxpkg.SlotTools:       "tools",
			ctxpkg.SlotSession:     "session",
			ctxpkg.SlotContext:     "context",
			ctxpkg.SlotUserContext: "",
			ctxpkg.SlotHandoff:     "",
		},
		Budgets: budgets,
		Stasher: newFakeStasher(),
	})

	for i, d := range plan.Decisions {
		if d.SlotName != ctxpkg.SlotOrder[i] {
			t.Errorf("decision[%d].SlotName = %q, want %q (SlotOrder mismatch)",
				i, d.SlotName, ctxpkg.SlotOrder[i])
		}
	}
}

func TestDecideAssembly_StashSeparatesIntentSkipFromPointerSubst(t *testing.T) {
	// Intent-driven skip and pointer-substitution both write to the
	// stash. Stash keys should match SlotName; the decision's ReasonTag
	// is the disambiguator.
	budgets := minimalBudgets()
	budgets[ctxpkg.SlotMemory] = 5 // pointer

	plan := DecideAssembly(context.Background(), AssemblyInput{
		Intent:    Intent{Type: IntentReviewSession}, // skip workspace context
		SlotOrder: ctxpkg.SlotOrder,
		Sources: map[string]string{
			ctxpkg.SlotMemory:  strings.Repeat("m", 1000),
			ctxpkg.SlotContext: "workspace context body",
		},
		Budgets: budgets,
		Stasher: newFakeStasher(),
	})

	if _, ok := plan.Stash[ctxpkg.SlotMemory]; !ok {
		t.Error("pointer substitution should stash original memory content")
	}
	if _, ok := plan.Stash[ctxpkg.SlotContext]; !ok {
		t.Error("intent-driven skip should stash workspace context content")
	}
}

func TestDecisionSummary_Format(t *testing.T) {
	plan := AssemblyPlan{
		Decisions: []SlotDecision{
			{SlotName: "universal", Action: ActionSkip},
			{SlotName: "system", Action: ActionShip},
			{SlotName: "agent", Action: ActionShip},
			{SlotName: "memory", Action: ActionPointer},
			{SlotName: "context", Action: ActionSkip},
		},
	}
	got := DecisionSummary(plan)
	// Sort within groups so the assertion is order-stable.
	if !strings.Contains(got, "ship=agent,system") {
		t.Errorf("ship group missing or wrong order: %q", got)
	}
	if !strings.Contains(got, "skip=context,universal") {
		t.Errorf("skip group missing or wrong order: %q", got)
	}
	if !strings.Contains(got, "pointer=memory") {
		t.Errorf("pointer group missing: %q", got)
	}
}

func TestSlotAction_String(t *testing.T) {
	cases := map[SlotAction]string{
		ActionShip:     "ship",
		ActionSkip:     "skip",
		ActionPointer:  "pointer",
		SlotAction(99): "unknown",
	}
	for action, want := range cases {
		if got := action.String(); got != want {
			t.Errorf("SlotAction(%d).String() = %q, want %q", action, got, want)
		}
	}
}

func TestContentByAction_FiltersBySlotAction(t *testing.T) {
	plan := AssemblyPlan{Decisions: []SlotDecision{
		{SlotName: "a", Action: ActionShip},
		{SlotName: "b", Action: ActionSkip},
		{SlotName: "c", Action: ActionShip},
		{SlotName: "d", Action: ActionPointer},
	}}
	ship := plan.ContentByAction(ActionShip)
	if len(ship) != 2 || ship[0].SlotName != "a" || ship[1].SlotName != "c" {
		t.Errorf("ContentByAction(Ship) = %+v, want [a, c]", ship)
	}
}
