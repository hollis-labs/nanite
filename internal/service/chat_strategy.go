package service

import (
	"context"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/classify"
	"github.com/hollis-labs/nanite/internal/grounding"
	"github.com/hollis-labs/nanite/internal/reflex"
	"github.com/hollis-labs/nanite/internal/strategy"
)

// strategyDecisionLogger is the chat service's narrow interface for the
// E3 strategy logger (CW-20260419-0026). *store.Store satisfies it.
// Defined locally so we don't have to grow the service.Store composite
// just for v1 strategy logging — the chat service holds the dependency
// directly, the same way reflex/grounding loggers are wired through
// SelfToolsTransport rather than the service.Store interface.
type strategyDecisionLogger interface {
	LogStrategyDecision(entry strategy.DecisionEntry) (int64, error)
}

// planStrategyForTurn is the chat service's strategy-loop entry point.
// It runs after classification (so ScopeTier / ExecutionPattern are
// already set on the loopState) and before the tool-use loop body.
//
// Steps:
//
//  1. Match the user input against the built-in reflex catalog. The
//     reflex match is read by PlanStrategy. A miss is fine — the
//     planner falls back on the M1 classification.
//  2. PlanStrategy returns a Strategy record naming an Approach + the
//     initial turn budget.
//  3. The Strategy is logged to strategy_decisions (best-effort —
//     errors do NOT gate dispatch).
//  4. The caller applies Strategy.MaxTurns to ls.limits.maxTurns
//     (E4 absorption — CW-20260419-0020).
//
// Grounding inputs are NOT consulted here in v1: the grounding recall
// step lives in mcp.SelfToolsTransport.callExecuteTask (E2 wire site)
// and only fires when the LLM dispatches a subagent. The strategy
// planner accepts a nil GroundingSignal and falls through to the
// intent-based rules.
//
// reflexSet may be nil (no overrides loaded) — we fall back to the
// builtin set in that case.
//
// logger may be nil — when nil, the strategy is still computed and
// applied but no row is written. This keeps the integration safe in
// test contexts that haven't wired the logger.
func planStrategyForTurn(
	ctx context.Context,
	sessionID, turnID, userContent string,
	scopeTier classify.ScopeTier,
	executionPattern classify.ExecutionPattern,
	reflexSet []reflex.Reflex,
	groundingResult *grounding.GroundingResult,
	logger strategyDecisionLogger,
) strategy.Strategy {
	if reflexSet == nil {
		reflexSet = reflex.BuiltinReflexes()
	}

	// 1. Reflex match — best-effort. A miss is the common case.
	var reflexSignal *strategy.ReflexSignal
	if match, ok := reflex.Match(userContent, scopeTier, executionPattern, reflexSet); ok {
		reflexSignal = &strategy.ReflexSignal{
			ReflexID:    match.Reflex.ID,
			HintTier:    match.HintTier,
			HintPattern: match.HintPattern,
			DispatchVia: match.Reflex.SideEffects.DispatchVia,
		}
	}

	// 2. Translate the optional grounding result into a strategy signal.
	var groundingSignal *strategy.GroundingSignal
	if groundingResult != nil && groundingResult.Enabled {
		groundingSignal = groundingSignalFromResult(*groundingResult)
	}

	// 3. Plan.
	strat := strategy.PlanStrategy(ctx, strategy.PlanInput{
		UserInput:        userContent,
		SessionID:        sessionID,
		TurnID:           turnID,
		ScopeTier:        scopeTier,
		ExecutionPattern: executionPattern,
		ReflexMatch:      reflexSignal,
		Grounding:        groundingSignal,
	})

	slog.Info("chat-service: strategy decision",
		"session_id", sessionID,
		"approach", string(strat.Approach),
		"max_turns", strat.MaxTurns,
		"reflex_match_id", strat.ReflexMatchID,
		"rationale", strat.Rationale,
	)

	// 4. Persist (best-effort).
	if logger != nil {
		if _, err := logger.LogStrategyDecision(strategy.FromStrategy(strat, sessionID, turnID)); err != nil {
			slog.Warn("chat-service: log strategy decision failed (non-fatal)", "err", err)
		}
	}

	return strat
}

// groundingSignalFromResult adapts a grounding.GroundingResult into the
// strategy package's local GroundingSignal. v1 maps:
//
//   - Enabled            → mirrored from the source.
//   - ConsultationIDs    → empty in v1 (the result the chat layer sees
//     does not carry IDs; the IDs are produced by LogConsultations in
//     mcp.callExecuteTask, which runs later).
//   - PrefersTerse       → true if any surfaced memory's Tags contain
//     "terse", "concise", or "brief".
//   - PriorSuccessApproach → empty in v1 (no heuristic to derive it).
func groundingSignalFromResult(r grounding.GroundingResult) *strategy.GroundingSignal {
	prefersTerse := false
	for _, m := range r.Surfaced {
		for _, tag := range m.Tags {
			switch tag {
			case "terse", "concise", "brief":
				prefersTerse = true
			}
		}
	}
	return &strategy.GroundingSignal{
		Enabled:      true,
		PrefersTerse: prefersTerse,
	}
}

// applyStrategyToLimits writes Strategy.MaxTurns into the loop's
// resolved limits, replacing the hard-coded defaultMaxTurns ceiling for
// this turn (E4 absorption).
//
// Edge cases:
//   - Strategy.MaxTurns <= 0 means the planner had no opinion; the
//     existing limits are left alone.
//   - Strategy.MaxTurns > hardCeiling is allowed at this layer — the
//     existing resolvedMaxTurns() clamp will cap it.
func applyStrategyToLimits(ls *loopState, strat strategy.Strategy) {
	if ls == nil || strat.MaxTurns <= 0 {
		return
	}
	ls.limits.maxTurns = strat.MaxTurns
}

// reviewExhaustedBudget runs the v1 single review step (turn-budget
// exhaustion) and returns the ReviewDecision.
//
// hasUsableData is the chat loop's signal that at least one tool
// result was load-bearing. v1 derives this from a successful tool
// call refs count > 0 — any successful tool call counts as usable.
// This is intentionally permissive; v2 will refine the heuristic (per
// ticket §"Mid-execution review" follow-ups).
func reviewExhaustedBudget(
	ctx context.Context,
	strat strategy.Strategy,
	turnsConsumed, maxTurns int,
	hasUsableData bool,
) strategy.ReviewDecision {
	remaining := maxTurns - turnsConsumed
	if remaining < 0 {
		remaining = 0
	}
	return strategy.ReviewMidExecution(ctx, strategy.TurnState{
		TurnsConsumed:  turnsConsumed,
		TurnsRemaining: remaining,
		HasUsableData:  hasUsableData,
		Strategy:       strat,
	})
}

// strategyHasUsableData reports whether the chat loop produced any
// load-bearing tool result this turn. v1 heuristic: at least one
// successful tool call. We walk ls.toolCallRefs and count entries
// whose Status is not "error".
func strategyHasUsableData(ls *loopState) bool {
	if ls == nil {
		return false
	}
	for _, ref := range ls.toolCallRefs {
		if ref.Status != "error" {
			return true
		}
	}
	return false
}

// strategy_pkg_ReviewAskToClarify is a thin re-export so the chat
// generation file can compare ReviewDecision values without growing
// its imports list further. Inlined for clarity at the call site.
func strategy_pkg_ReviewAskToClarify() strategy.ReviewDecision {
	return strategy.ReviewAskToClarify
}

// strategyClarifyingQuestion returns the v1 fixed-template clarifying
// question to inject when the budget-exhaustion review decides we
// shouldn't fabricate. Wraps strategy.ClarifyingQuestion so the
// generation file doesn't need to import the strategy package directly.
func strategyClarifyingQuestion(userContent string, tier classify.ScopeTier, strat strategy.Strategy) string {
	return strategy.ClarifyingQuestion(strategy.PlanInput{
		UserInput: userContent,
		ScopeTier: tier,
	}, strat)
}
