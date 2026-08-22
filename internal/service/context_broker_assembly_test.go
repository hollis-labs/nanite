package service

import (
	"context"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/nanite/internal/contextbroker"
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
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := s.CreateMessage(context.Background(), &store.Message{
		ID:        "plan-msg",
		SessionID: sess.ID,
		Role:      "user",
		Content:   "implement a new dev_grep helper",
	}); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	agent := &store.AgentProfile{ID: "plan-agent", Slug: "plan", Status: "active", SystemPrompt: "p"}

	result, err := svc.AssembleSlots(context.Background(), sess, agent, nil, "", 200000, "")
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
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	agent := &store.AgentProfile{ID: "prefix-agent", Slug: "p", Status: "active", SystemPrompt: "p"}

	// Turn 1 — user asks to write code (intent=write_code → context slot ships if present).
	if err := s.CreateMessage(context.Background(), &store.Message{ID: "t1", SessionID: sess.ID, Role: "user", Content: "write a new helper function"}); err != nil {
		t.Fatalf("CreateMessage t1: %v", err)
	}
	res1, err := svc.AssembleSlots(context.Background(), sess, agent, nil, "", 200000, "")
	if err != nil {
		t.Fatalf("turn 1: %v", err)
	}

	// Turn 2 — user asks to review (intent=review_session → context slot skipped).
	if err := s.CreateMessage(context.Background(), &store.Message{ID: "t2", SessionID: sess.ID, Role: "user", Content: "review our session history"}); err != nil {
		t.Fatalf("CreateMessage t2: %v", err)
	}
	res2, err := svc.AssembleSlots(context.Background(), sess, agent, nil, "", 200000, "")
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

// TestDecideAssembly_SkipEmitsEmptyContentAndStashes asserts that when
// the decider skips a slot (because the per-turn intent doesn't need it),
// the slot's decision carries empty content (so downstream Assemble drops
// it from the wire, preserving cacheable prefix savings) and the original
// content is moved to plan.Stash for recovery.
func TestDecideAssembly_SkipEmitsEmptyContentAndStashes(t *testing.T) {
	// We exercise contextbroker.DecideAssembly directly here because
	// populating SlotContext from the real broker requires a live
	// Vanta/PCC/Conduit wiring. The decision logic is the unit under test;
	// end-to-end integration with AssembleSlots → SlotBlocks is covered by
	// TestAssembleSlots_PlanReachesResult.
	plan := contextbroker.DecideAssembly(context.Background(), contextbroker.AssemblyInput{
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

	// Use a fake stasher so the decider emits ActionPointer (no real
	// artifact store wired here — the stash test in
	// contextbroker/assembly_test.go covers the artifact-id format).
	plan := contextbroker.DecideAssembly(context.Background(), contextbroker.AssemblyInput{
		Intent:    contextbroker.Intent{Type: contextbroker.IntentCustom},
		SlotOrder: ctxpkg.SlotOrder,
		SessionID: "pointer-test",
		Sources: map[string]string{
			ctxpkg.SlotMemory: strings.Repeat("memory body ", 200),
		},
		Budgets: budgets,
		Stasher: &fakeArtifactStasher{},
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
	if !strings.Contains(memDec.Content, "<ref:artifact_id=") {
		t.Errorf("pointer marker malformed: %q", memDec.Content)
	}
}

// TestAssembleSlots_UniversalSlotEmptyWhenAbsent exercises the decider's
// position-stability contract when SlotUniversal source is empty (e.g. an
// integration test calling DecideAssembly directly without
// chat.AssembleSlotSources). Even with empty content, the decision is
// emitted at position 0 — Anthropic's cacheable_prefix_tokens math depends
// on positional stability.
//
// The production wire-up (chat.AssembleSlotSources sourcing from
// chat.UniversalRulesBlock()) is covered by
// TestAssembleSlots_UniversalSlotShipsContent below.
func TestAssembleSlots_UniversalSlotEmptyWhenAbsent(t *testing.T) {
	plan := contextbroker.DecideAssembly(context.Background(), contextbroker.AssemblyInput{
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

// TestAssembleSlots_UniversalSlotShipsContent is the CW-20260512-0114
// end-to-end smoke test: chat.AssembleSlotSources sources the universal
// block from chat.UniversalRulesBlock() and the Context Broker ships it at
// position 0 of the assembled SlotBlocks. Together these prove the slot
// reaches the wire for every dispatch type — chat sessions and subagent
// sessions alike use this same code path.
//
// The "subagent" framing of the smoke test is the empty-system-prompt
// profile shape: pre-CW-20260512-0100, an agent with empty SystemPrompt
// and no template received zero universal rules. Post-CW-20260512-0114,
// the rules ride on SlotUniversal — independent of profile body or
// template assignment.
func TestAssembleSlots_UniversalSlotShipsContent(t *testing.T) {
	s, err := store.New(context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	client := chat.NewContextClient(s)
	svc := NewContextService(ContextServiceConfig{Client: client})

	sess := &store.Session{ID: "subagent-sess"}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// Empty SystemPrompt mirrors the c160 researcher subagent that
	// previously fabricated 8.1KB of analysis when given no codebase
	// access. The universal-rules slot is the structural fix.
	subagentProfile := &store.AgentProfile{
		ID:           "researcher-subagent",
		Slug:         "researcher",
		Status:       "active",
		SystemPrompt: "",
	}

	result, err := svc.AssembleSlots(context.Background(), sess, subagentProfile, nil, "", 200000, "")
	if err != nil {
		t.Fatalf("AssembleSlots: %v", err)
	}

	// Decision 0 must be SlotUniversal, must be ActionShip, and must
	// carry the canonical UniversalRulesBlock content.
	d0 := result.Plan.Decisions[0]
	if d0.SlotName != ctxpkg.SlotUniversal {
		t.Fatalf("position 0 must be SlotUniversal, got %q", d0.SlotName)
	}
	if d0.Action != contextbroker.ActionShip {
		t.Errorf("SlotUniversal must ship for subagent dispatch, got action %v (reason %q)", d0.Action, d0.ReasonTag)
	}
	if d0.Content == "" {
		t.Fatal("SlotUniversal content empty — universal rules failed to reach the wire for subagent dispatch")
	}
	if d0.Content != chat.UniversalRulesBlock() {
		t.Error("SlotUniversal content drifted from chat.UniversalRulesBlock — content must be sourced verbatim")
	}

	// The assembled SlotBlocks must include the universal block at the
	// leading wire position. ContextWindow.Assemble drops empty slots, so
	// SlotUniversal is the first non-empty block.
	if len(result.Blocks) == 0 {
		t.Fatal("no slot blocks assembled — subagent dispatch produced empty wire")
	}
	if result.Blocks[0].SlotName != ctxpkg.SlotUniversal {
		t.Errorf("first wire block must be SlotUniversal, got %q", result.Blocks[0].SlotName)
	}
	if !strings.Contains(result.Blocks[0].Content, "Refuse rather than fabricate") {
		t.Error("first wire block missing universal-rules refusal clause — content drifted")
	}

	// Window slot tokens must report SlotUniversal distinct from
	// SlotSystem. This is what the request_build slog walks (per
	// chat_generate.go); a non-zero count here means
	// `universal_tokens` will appear separately from `system_tokens`.
	uniSlot := result.Window.Slot(ctxpkg.SlotUniversal)
	if uniSlot == nil {
		t.Fatal("ContextWindow missing SlotUniversal — request_build slog cannot report it")
	}
	if uniSlot.TokenCount == 0 {
		t.Error("SlotUniversal.TokenCount = 0 with non-empty content — token estimator broke")
	}
	sysSlot := result.Window.Slot(ctxpkg.SlotSystem)
	if sysSlot != nil && strings.Contains(sysSlot.Content, "Refuse rather than fabricate") {
		t.Error("SlotSystem must NOT carry the universal-rules block post-CW-20260512-0114 — drift would double-ship cacheable content")
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
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := s.CreateMessage(context.Background(), &store.Message{ID: "c1", SessionID: sess.ID, Role: "user", Content: "hi"}); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	agent := &store.AgentProfile{ID: "conv-agent", Slug: "c", Status: "active", SystemPrompt: "p"}

	result, err := svc.AssembleSlots(context.Background(), sess, agent, []llmtypes.ToolDefinition{}, "", 200000, "")
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

// fakeArtifactStasher is a no-op stasher used by service-layer tests that
// don't wire a real artifact store. It returns a deterministic ID matching
// the contextbroker.DeterministicArtifactID convention so pointer envelopes
// have stable shape across runs. SP-20260512-0008 W2C (CW-20260512-0110).
type fakeArtifactStasher struct{}

func (fakeArtifactStasher) StashSlot(_ context.Context, req contextbroker.StashRequest) (contextbroker.StashResult, error) {
	return contextbroker.StashResult{
		ArtifactID: contextbroker.DeterministicArtifactID(req.SessionID, req.SlotName, req.Content),
	}, nil
}
