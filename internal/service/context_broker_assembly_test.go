package service

import (
	"context"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/contextbroker"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestAssembleSlots_PlanReachesResult locks down the W1A wiring:
// AssembleSlots must surface the broker's per-turn AssemblyPlan on the
// returned SlotAssemblyResult so downstream telemetry / future consumers
// can observe (and audit) ship/skip/pointer decisions.
//
// SP-20260512-0008 W1A (CW-20260512-0104).
func TestAssembleSlots_PlanReachesResult(t *testing.T) {
	s, err := store.New(context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	client := chat.NewContextClient(s)
	svc := NewContextService(ContextServiceConfig{Client: client})

	sess := &store.Session{ID: "plan-sess"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := s.CreateMessage(&store.Message{
		ID:        "plan-msg",
		SessionID: sess.ID,
		Role:      "user",
		Content:   "implement a new dev_grep helper",
	}); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	agent := &store.AgentProfile{ID: "plan-agent", Slug: "plan", Status: "active", SystemPrompt: "p"}

	result, err := svc.AssembleSlots(context.Background(), sess, agent, &store.AgentMode{}, nil, nil, "", 200000, nil, "")
	if err != nil {
		t.Fatalf("AssembleSlots: %v", err)
	}

	// Plan must emit one decision per SlotOrder entry.
	if len(result.Plan.Decisions) != len(ctxpkg.SlotOrder) {
		t.Fatalf("plan.Decisions = %d, want %d (one per SlotOrder)", len(result.Plan.Decisions), len(ctxpkg.SlotOrder))
	}
	if result.Plan.Decisions[0].SlotName != ctxpkg.SlotUniversal {
		t.Errorf("plan position 0 must be SlotUniversal, got %q", result.Plan.Decisions[0].SlotName)
	}
}

// TestAssembleSlots_StableCachePrefix_AcrossTurns confirms the assembly
// decider preserves the leading slot ORDER (positions 0..N) across two
// consecutive turns even when content varies. Cache-prefix stability is
// the load-bearing W1A contract — the wire shape of slot positions must
// not depend on the per-turn content.
func TestAssembleSlots_StableCachePrefix_AcrossTurns(t *testing.T) {
	s, err := store.New(context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	client := chat.NewContextClient(s)
	svc := NewContextService(ContextServiceConfig{Client: client})

	sess := &store.Session{ID: "prefix-sess"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	agent := &store.AgentProfile{ID: "prefix-agent", Slug: "p", Status: "active", SystemPrompt: "p"}

	// Turn 1 — user asks to write code (intent=write_code → context slot ships if present).
	if err := s.CreateMessage(&store.Message{ID: "t1", SessionID: sess.ID, Role: "user", Content: "write a new helper function"}); err != nil {
		t.Fatalf("CreateMessage t1: %v", err)
	}
	res1, err := svc.AssembleSlots(context.Background(), sess, agent, &store.AgentMode{}, nil, nil, "", 200000, nil, "")
	if err != nil {
		t.Fatalf("turn 1: %v", err)
	}

	// Turn 2 — user asks to review (intent=review_session → context slot skipped).
	if err := s.CreateMessage(&store.Message{ID: "t2", SessionID: sess.ID, Role: "user", Content: "review our session history"}); err != nil {
		t.Fatalf("CreateMessage t2: %v", err)
	}
	res2, err := svc.AssembleSlots(context.Background(), sess, agent, &store.AgentMode{}, nil, nil, "", 200000, nil, "")
	if err != nil {
		t.Fatalf("turn 2: %v", err)
	}

	if len(res1.Plan.Decisions) != len(res2.Plan.Decisions) {
		t.Fatalf("decision count varies turn-to-turn: %d vs %d", len(res1.Plan.Decisions), len(res2.Plan.Decisions))
	}
	for i := range res1.Plan.Decisions {
		if res1.Plan.Decisions[i].SlotName != res2.Plan.Decisions[i].SlotName {
			t.Errorf("position %d slot diverged: turn1=%q turn2=%q (cache prefix broken)",
				i, res1.Plan.Decisions[i].SlotName, res2.Plan.Decisions[i].SlotName)
		}
	}
}

// TestAssembleSlots_SkippedSlotAbsentFromBlocks confirms that when the
// decider skips a slot (because the per-turn intent doesn't need it),
// that slot is absent from the assembled SlotBlocks shipped to the
// provider — preserving cacheable prefix savings.
func TestAssembleSlots_SkippedSlotAbsentFromBlocks(t *testing.T) {
	// We exercise the decider directly here because populating SlotContext
	// from the real broker requires a live Vanta/PCC/Conduit wiring. The
	// decision logic is the unit under test; integration into AssembleSlots
	// is covered by TestAssembleSlots_PlanReachesResult.
	plan := contextbroker.DecideAssembly(contextbroker.AssemblyInput{
		Intent:    contextbroker.Intent{Type: contextbroker.IntentReviewSession},
		SlotOrder: ctxpkg.SlotOrder,
		Sources: map[string]string{
			ctxpkg.SlotSystem:  "system content",
			ctxpkg.SlotContext: "workspace context content",
		},
		Budgets: ctxpkg.DefaultBudgets(),
	})

	var ctxDec contextbroker.SlotDecision
	for _, d := range plan.Decisions {
		if d.SlotName == ctxpkg.SlotContext {
			ctxDec = d
		}
	}
	if ctxDec.Action != contextbroker.ActionSkip {
		t.Fatalf("expected SlotContext ActionSkip for review_session, got %v", ctxDec.Action)
	}
	if ctxDec.Content != "" {
		t.Errorf("skipped slot should emit empty content for wire-level absence, got %q", ctxDec.Content)
	}
	if plan.Stash[ctxpkg.SlotContext] != "workspace context content" {
		t.Errorf("skipped content should be in plan.Stash for recovery, got %q", plan.Stash[ctxpkg.SlotContext])
	}
}

// TestAssembleSlots_PointerSubstitutionStillShipsAtPosition confirms an
// oversized slot replaced by a pointer keeps its position in the assembled
// blocks (just with smaller content), preserving cache shape.
func TestAssembleSlots_PointerSubstitutionStillShipsAtPosition(t *testing.T) {
	budgets := ctxpkg.DefaultBudgets()
	budgets[ctxpkg.SlotMemory] = 5 // force pointer

	plan := contextbroker.DecideAssembly(contextbroker.AssemblyInput{
		Intent:    contextbroker.Intent{Type: contextbroker.IntentCustom},
		SlotOrder: ctxpkg.SlotOrder,
		Sources: map[string]string{
			ctxpkg.SlotMemory: strings.Repeat("memory body ", 200),
		},
		Budgets: budgets,
	})

	var memDec contextbroker.SlotDecision
	for _, d := range plan.Decisions {
		if d.SlotName == ctxpkg.SlotMemory {
			memDec = d
		}
	}
	if memDec.Action != contextbroker.ActionPointer {
		t.Fatalf("expected memory ActionPointer, got %v", memDec.Action)
	}
	if !strings.Contains(memDec.Content, "<ref:slot=memory,") {
		t.Errorf("pointer marker malformed: %q", memDec.Content)
	}
}

// TestAssembleSlots_UniversalSlotReservedEmpty exercises the W1A contract
// that the universal slot is always present at position 0 in the plan
// even when content is empty. Sprint 2 / T2.4 wires the content; until
// then the slot is reserved with empty content (skipped at wire level
// but tracked at the decider layer).
func TestAssembleSlots_UniversalSlotReservedEmpty(t *testing.T) {
	plan := contextbroker.DecideAssembly(contextbroker.AssemblyInput{
		Intent:    contextbroker.Intent{Type: contextbroker.IntentCustom},
		SlotOrder: ctxpkg.SlotOrder,
		Sources:   map[string]string{ctxpkg.SlotSystem: "sys"},
		Budgets:   ctxpkg.DefaultBudgets(),
	})

	if plan.Decisions[0].SlotName != ctxpkg.SlotUniversal {
		t.Fatalf("position 0 must be SlotUniversal, got %q", plan.Decisions[0].SlotName)
	}
	if plan.Decisions[0].Content != "" {
		t.Errorf("empty universal slot should emit empty content, got %q", plan.Decisions[0].Content)
	}
}

// TestAssembleSlots_ConversationNotInPlanDecider asserts that conversation
// history is not subject to the decider — messages are always serialized
// and shipped. The decider's decision for SlotConversation is empty
// because the service-layer wiring excludes it from the decider's input
// map and sets it on the window directly.
func TestAssembleSlots_ConversationNotInPlanDecider(t *testing.T) {
	s, err := store.New(context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	client := chat.NewContextClient(s)
	svc := NewContextService(ContextServiceConfig{Client: client})

	sess := &store.Session{ID: "conv-sess"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := s.CreateMessage(&store.Message{ID: "c1", SessionID: sess.ID, Role: "user", Content: "hi"}); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	agent := &store.AgentProfile{ID: "conv-agent", Slug: "c", Status: "active", SystemPrompt: "p"}

	result, err := svc.AssembleSlots(context.Background(), sess, agent, &store.AgentMode{}, nil, []llmtypes.ToolDefinition{}, "", 200000, nil, "")
	if err != nil {
		t.Fatalf("AssembleSlots: %v", err)
	}

	// The window must still have conversation content (set directly).
	convSlot := result.Window.Slot(ctxpkg.SlotConversation)
	if convSlot == nil || convSlot.Content == "" {
		t.Error("conversation slot should be populated by service layer outside the decider")
	}
	// The plan's conversation decision (last position) should be present
	// with empty source content — the source map intentionally omits
	// conversation, so the decider sees empty content and skips.
	var convDecision contextbroker.SlotDecision
	for _, d := range result.Plan.Decisions {
		if d.SlotName == ctxpkg.SlotConversation {
			convDecision = d
		}
	}
	if convDecision.SlotName == "" {
		t.Fatal("plan should still emit a SlotConversation decision for position stability")
	}
	if convDecision.Action != contextbroker.ActionSkip {
		t.Errorf("conversation decision should be ActionSkip in the decider's view (handled outside the plan), got %v", convDecision.Action)
	}
}
