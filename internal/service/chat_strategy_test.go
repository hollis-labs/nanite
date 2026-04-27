package service

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/classify"
	"github.com/hollis-labs/nanite/internal/grounding"
	"github.com/hollis-labs/nanite/internal/strategy"
)

// recordingStrategyLogger captures every DecisionEntry the chat layer
// would persist. Used by the integration test below to assert the
// strategy planner ran and produced an entry with the expected
// approach/budget/reflex_match_id.
type recordingStrategyLogger struct {
	mu      sync.Mutex
	entries []strategy.DecisionEntry
}

func (r *recordingStrategyLogger) LogStrategyDecision(entry strategy.DecisionEntry) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, entry)
	return int64(len(r.entries)), nil
}

func (r *recordingStrategyLogger) get(i int) strategy.DecisionEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.entries[i]
}

// TestPlanStrategyForTurn_LogsAndAppliesBudget verifies the wire site:
// classification + reflex match are read, a Strategy is produced, the
// logger records the entry, and the returned MaxTurns is the
// intent-derived budget.
func TestPlanStrategyForTurn_LogsAndAppliesBudget(t *testing.T) {
	logger := &recordingStrategyLogger{}
	strat := planStrategyForTurn(
		context.Background(),
		"sess-1", "msg-1", "implement the new module",
		classify.TierMedium,
		classify.PatternInline,
		nil, /* reflexSet */
		nil, /* groundingResult */
		logger,
	)
	if strat.Approach != strategy.ApproachDirectChain {
		// The reflex catalog includes "implement" → worker-execute
		// (Pattern: worker, dispatch_via: executeTask). For TierMedium
		// + the worker-execute reflex, the v1 planner's Rule 3
		// (intent-based) keeps direct_chain because the reflex is
		// advisory (no PatternBackground). The reflex ID still gets
		// recorded.
		t.Fatalf("approach: got %q, want direct_chain", strat.Approach)
	}
	if strat.MaxTurns != strategy.BudgetMedium {
		t.Fatalf("max_turns: got %d, want %d", strat.MaxTurns, strategy.BudgetMedium)
	}
	if strat.ReflexMatchID == "" {
		t.Errorf("expected non-empty reflex match id (worker-execute should fire on 'implement')")
	}
	if len(logger.entries) != 1 {
		t.Fatalf("logger entries: got %d, want 1", len(logger.entries))
	}
	got := logger.get(0)
	if got.SessionID != "sess-1" || got.TurnID != "msg-1" {
		t.Errorf("session/turn id: got %q/%q", got.SessionID, got.TurnID)
	}
	if got.MaxTurns != strategy.BudgetMedium {
		t.Errorf("logged max_turns: got %d, want %d", got.MaxTurns, strategy.BudgetMedium)
	}
}

// TestPlanStrategyForTurn_BackgroundReflex_UpgradesToSubagent verifies
// "in the background" routes through the background-long-task reflex
// (when the M1 classifier produced PatternBackground) and forces
// subagent_delegation with the large budget.
func TestPlanStrategyForTurn_BackgroundReflex_UpgradesToSubagent(t *testing.T) {
	logger := &recordingStrategyLogger{}
	// Note: the reflex matcher requires ExecutionPattern to align with
	// the reflex's ExecutionPatternHint when the hint is set. The
	// background-long-task reflex hints PatternBackground; in real
	// use the M1 classifier produces PatternBackground from phrases
	// like "in the background". Here we pass it explicitly so the
	// test pins the planner's behavior, not the classifier's.
	strat := planStrategyForTurn(
		context.Background(),
		"sess-2", "msg-2",
		"crawl the entire docs site in the background",
		classify.TierMedium,
		classify.PatternBackground,
		nil, nil,
		logger,
	)
	if strat.Approach != strategy.ApproachSubagentDelegation {
		t.Fatalf("approach: got %q, want subagent_delegation", strat.Approach)
	}
	if strat.MaxTurns != strategy.BudgetLarge {
		t.Fatalf("max_turns: got %d, want %d", strat.MaxTurns, strategy.BudgetLarge)
	}
	if got := logger.get(0); got.Approach != strategy.ApproachSubagentDelegation {
		t.Errorf("logged approach mismatch: %q", got.Approach)
	}
}

// TestPlanStrategyForTurn_NilLogger_StillReturnsStrategy verifies the
// nil-logger code path: planning still produces a Strategy and no
// panic.
func TestPlanStrategyForTurn_NilLogger_StillReturnsStrategy(t *testing.T) {
	strat := planStrategyForTurn(
		context.Background(),
		"sess-3", "", "what time is it",
		classify.TierTrivial,
		classify.PatternInline,
		nil, nil, nil,
	)
	if strat.MaxTurns != strategy.BudgetTrivial {
		t.Fatalf("max_turns: got %d, want %d", strat.MaxTurns, strategy.BudgetTrivial)
	}
}

// TestApplyStrategyToLimits_OverridesDefault verifies the Strategy.MaxTurns
// replaces the loop's resolved cap (E4 absorption). The default
// loopState.limits.maxTurns starts at defaultMaxTurns (75); we want
// applyStrategyToLimits to overwrite that with the planner's value.
func TestApplyStrategyToLimits_OverridesDefault(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	if ls.resolvedMaxTurns() != defaultMaxTurns {
		t.Fatalf("baseline resolvedMaxTurns: got %d, want %d", ls.resolvedMaxTurns(), defaultMaxTurns)
	}
	applyStrategyToLimits(ls, strategy.Strategy{MaxTurns: 12})
	if ls.resolvedMaxTurns() != 12 {
		t.Errorf("after applyStrategyToLimits: got %d, want 12", ls.resolvedMaxTurns())
	}
}

// TestApplyStrategyToLimits_NoOpOnZeroMaxTurns verifies that a
// Strategy with MaxTurns == 0 leaves limits unchanged (defensive).
func TestApplyStrategyToLimits_NoOpOnZeroMaxTurns(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	original := ls.limits.maxTurns
	applyStrategyToLimits(ls, strategy.Strategy{MaxTurns: 0})
	if ls.limits.maxTurns != original {
		t.Errorf("limits.maxTurns mutated unexpectedly: got %d, want %d", ls.limits.maxTurns, original)
	}
}

// TestReviewExhaustedBudget_NoUsableData_AskToClarify is the
// load-bearing integration assertion: a turn that ran through max_turns
// without any successful tool result triggers ask_to_clarify, which is
// the "ask, don't fabricate" enforcement.
func TestReviewExhaustedBudget_NoUsableData_AskToClarify(t *testing.T) {
	strat := strategy.Strategy{MaxTurns: 10}
	d := reviewExhaustedBudget(context.Background(), strat,
		10 /* turnsConsumed */, 10 /* maxTurns */, false /* hasUsableData */)
	if d != strategy.ReviewAskToClarify {
		t.Fatalf("decision: got %q, want %q", d, strategy.ReviewAskToClarify)
	}
}

// TestReviewExhaustedBudget_HasUsableData_WrapPartial verifies the
// partial-data wrap path.
func TestReviewExhaustedBudget_HasUsableData_WrapPartial(t *testing.T) {
	strat := strategy.Strategy{MaxTurns: 10}
	d := reviewExhaustedBudget(context.Background(), strat, 10, 10, true)
	if d != strategy.ReviewWrapWithPartialData {
		t.Fatalf("decision: got %q, want %q", d, strategy.ReviewWrapWithPartialData)
	}
}

// TestStrategyHasUsableData reads off the loopState.toolCallRefs the
// way the chat loop populates it, and verifies the heuristic flips
// "false → true" when at least one non-error ref is present.
func TestStrategyHasUsableData(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	if strategyHasUsableData(ls) {
		t.Errorf("expected false on empty refs")
	}
	ls.toolCallRefs = append(ls.toolCallRefs, chat.ToolCallRef{ID: "t1", Status: "error"})
	if strategyHasUsableData(ls) {
		t.Errorf("expected false when only error refs are present")
	}
	ls.toolCallRefs = append(ls.toolCallRefs, chat.ToolCallRef{ID: "t2", Status: "success"})
	if !strategyHasUsableData(ls) {
		t.Errorf("expected true once at least one success ref is present")
	}
}

// TestBudgetExhaustionPath_EndToEnd is the strawman integration test
// from the ticket §"What done looks like" #5: a turn that runs
// through max_turns without producing any usable data MUST yield an
// ask_to_clarify clarifying message instead of a fabricated synthesis.
//
// We exercise the SAME code path the chat loop invokes (planStrategyForTurn
// → applyStrategyToLimits → loop runs to budget → reviewExhaustedBudget
// → strategyClarifyingQuestion) without spinning up the full provider
// stack. The assertions:
//
//   1. PlanStrategy produced a budget of 20 (TierMedium default).
//   2. The loop "ran" 20 times (turnsConsumed == maxTurns).
//   3. With no successful tool calls, the review returned ask_to_clarify.
//   4. The clarifying-question message names what's missing in plain
//      English (the user's topic, the budget, a narrowing prompt).
//   5. The message is NOT a fabricated synthesis (it doesn't pretend
//      to answer; it asks).
func TestBudgetExhaustionPath_EndToEnd(t *testing.T) {
	logger := &recordingStrategyLogger{}
	userInput := "Find every TODO in the repo and explain what each one means."

	// Stage 1: plan the strategy as the chat loop would.
	strat := planStrategyForTurn(
		context.Background(),
		"sess-x", "msg-x", userInput,
		classify.TierMedium,
		classify.PatternInline,
		nil,
		(*grounding.GroundingResult)(nil),
		logger,
	)
	if strat.MaxTurns != strategy.BudgetMedium {
		t.Fatalf("planned max_turns: got %d, want %d", strat.MaxTurns, strategy.BudgetMedium)
	}

	// Stage 2: the loop runs to max_turns without any successful tool
	// call (simulated by not pushing anything into ls.toolCallRefs).
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	applyStrategyToLimits(ls, strat)
	ls.iteration = ls.resolvedMaxTurns() // simulate loop exhaustion.

	// Stage 3: the review fires.
	hasUsableData := strategyHasUsableData(ls)
	if hasUsableData {
		t.Fatalf("expected hasUsableData=false (no successful tool calls)")
	}
	decision := reviewExhaustedBudget(context.Background(), strat,
		ls.iteration, ls.resolvedMaxTurns(), hasUsableData)
	if decision != strategy.ReviewAskToClarify {
		t.Fatalf("review decision: got %q, want %q (the ask-don't-fabricate path)",
			decision, strategy.ReviewAskToClarify)
	}

	// Stage 4: the clarifying question is generated.
	q := strategyClarifyingQuestion(userInput, classify.TierMedium, strat)
	if q == "" {
		t.Fatalf("clarifying question is empty")
	}

	// Stage 5: assertions on the clarifying message shape.
	mustContain := []string{
		"didn't gather enough", // signals "ask, don't fabricate"
		"Find every TODO",      // echoes the user topic
	}
	for _, want := range mustContain {
		if !strings.Contains(q, want) {
			t.Errorf("clarifying message missing %q: %s", want, q)
		}
	}
	// Negative assertion: the message must NOT pretend to answer.
	mustNotContain := []string{
		"Here are the TODOs",
		"Here is what each TODO means",
	}
	for _, ban := range mustNotContain {
		if strings.Contains(q, ban) {
			t.Errorf("clarifying message looks like a fabricated answer (contains %q): %s", ban, q)
		}
	}

	// Stage 6: the strategy decision was logged for tuning.
	if len(logger.entries) != 1 {
		t.Fatalf("logger entries: got %d, want 1", len(logger.entries))
	}
	if logger.get(0).Approach != strategy.ApproachDirectChain {
		t.Errorf("logged approach: got %q, want direct_chain", logger.get(0).Approach)
	}
}
