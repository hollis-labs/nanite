package strategy

import (
	"context"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/classify"
)

// PlanStrategy returns the v1 strategy decision for a turn. It does NOT
// call an LLM — the planner is rule-based, cheap, and synchronous. The
// decision tree (per ticket §"Discussion prompts" #5):
//
//  1. If a reflex match indicates background execution (DispatchVia ==
//     "executeBackground" OR HintPattern == PatternBackground), choose
//     ApproachSubagentDelegation with the Large budget — background
//     work is execution-shaped and earns the larger budget.
//  2. If grounding signals a prior-success approach for a similar ask,
//     adopt it (and record the consultation IDs as evidence).
//  3. Otherwise, pick from the M1 intent classification:
//     TierTrivial            → direct_chain, MaxTurns=10
//     TierSmall, TierMedium  → direct_chain, MaxTurns=20
//     TierLarge, TierOpen    → subagent_delegation, MaxTurns=40
//  4. Default fallback: direct_chain, MaxTurns=20 (TierMedium-equivalent).
//
// PlanStrategy never returns an error — every input shape produces a
// valid Strategy. ctx is accepted for future LLM-backed v2 planning;
// v1 ignores it.
func PlanStrategy(ctx context.Context, in PlanInput) Strategy {
	_ = ctx // reserved for v2

	now := time.Now()

	// Pull reflex evidence first; the strategy may need to record the
	// matched ID even when other rules pick the approach.
	reflexID := ""
	var reflexHintPattern classify.ExecutionPattern
	var reflexDispatchVia string
	if in.ReflexMatch != nil {
		reflexID = in.ReflexMatch.ReflexID
		reflexHintPattern = in.ReflexMatch.HintPattern
		reflexDispatchVia = in.ReflexMatch.DispatchVia
	}

	// Grounding evidence: collect consultation IDs and the optional
	// prior-success approach.
	var groundingIDs []int64
	priorApproach := Approach("")
	prefersTerse := false
	if in.Grounding != nil && in.Grounding.Enabled {
		groundingIDs = in.Grounding.ConsultationIDs
		priorApproach = in.Grounding.PriorSuccessApproach
		prefersTerse = in.Grounding.PrefersTerse
	}

	rationaleParts := make([]string, 0, 4)

	// Rule 1 — reflex says background.
	if reflexHintPattern == classify.PatternBackground || reflexDispatchVia == "executeBackground" {
		rationaleParts = append(rationaleParts,
			"reflex match "+reflexID+" → background execution")
		if prefersTerse {
			rationaleParts = append(rationaleParts, "user prefers terse replies")
		}
		return Strategy{
			Approach:                 ApproachSubagentDelegation,
			Rationale:                strings.Join(rationaleParts, "; "),
			MaxTurns:                 BudgetLarge,
			ReflexMatchID:            reflexID,
			GroundingConsultationIDs: groundingIDs,
			CreatedAt:                now,
		}
	}

	// Rule 2 — grounding adopted prior-success approach.
	if priorApproach != "" && approachIsValid(priorApproach) {
		rationaleParts = append(rationaleParts,
			"grounding: prior similar turn succeeded with "+string(priorApproach))
		if prefersTerse {
			rationaleParts = append(rationaleParts, "user prefers terse replies")
		}
		return Strategy{
			Approach:                 priorApproach,
			Rationale:                strings.Join(rationaleParts, "; "),
			MaxTurns:                 budgetForApproach(priorApproach),
			ReflexMatchID:            reflexID,
			GroundingConsultationIDs: groundingIDs,
			CreatedAt:                now,
		}
	}

	// Rule 3 — intent classification.
	approach, budget, intentRationale := approachForIntent(in.ScopeTier, in.ExecutionPattern)
	rationaleParts = append(rationaleParts, intentRationale)
	if reflexID != "" {
		// A reflex matched but didn't trigger a rule above; record it
		// in the rationale so analytics can see the link.
		rationaleParts = append(rationaleParts, "reflex match "+reflexID+" (advisory)")
	}
	if prefersTerse {
		rationaleParts = append(rationaleParts, "user prefers terse replies")
	}

	return Strategy{
		Approach:                 approach,
		Rationale:                strings.Join(rationaleParts, "; "),
		MaxTurns:                 budget,
		ReflexMatchID:            reflexID,
		GroundingConsultationIDs: groundingIDs,
		CreatedAt:                now,
	}
}

// approachForIntent maps M1 classification to (approach, budget,
// rationale). When tier is TierInvalid (classifier didn't run), we fall
// back to direct_chain + medium budget per the documented default.
func approachForIntent(tier classify.ScopeTier, pattern classify.ExecutionPattern) (Approach, int, string) {
	switch tier {
	case classify.TierTrivial:
		return ApproachDirectChain, BudgetTrivial, "intent: trivial → direct_chain"
	case classify.TierSmall, classify.TierMedium:
		return ApproachDirectChain, BudgetMedium, "intent: " + tier.String() + " → direct_chain"
	case classify.TierLarge, classify.TierOpen:
		// Background pattern is handled in Rule 1 above; here a
		// large/open intent without a background reflex still means
		// subagent dispatch is the right shape.
		_ = pattern
		return ApproachSubagentDelegation, BudgetLarge, "intent: " + tier.String() + " → subagent_delegation"
	default:
		return ApproachDirectChain, DefaultBudget, "intent: invalid/unset → direct_chain (default)"
	}
}

// budgetForApproach returns the default turn budget for an Approach when
// no other signal sets it. Used when adopting a prior-success approach
// from grounding without a specific budget hint.
func budgetForApproach(a Approach) int {
	switch a {
	case ApproachDirectChain:
		return BudgetMedium
	case ApproachSubagentDelegation:
		return BudgetLarge
	case ApproachAskToClarify:
		// Asking is cheap — the loop never runs the full budget here.
		return BudgetTrivial
	case ApproachAlreadyAnsweredFromCache:
		// Cache hit; no loop expected.
		return BudgetTrivial
	default:
		return DefaultBudget
	}
}

// approachIsValid reports whether a is a recognised Approach. Used by
// PlanStrategy when reading the prior-success hint from grounding so a
// stale enum value can't poison the budget.
func approachIsValid(a Approach) bool {
	switch a {
	case ApproachDirectChain,
		ApproachSubagentDelegation,
		ApproachAskToClarify,
		ApproachAlreadyAnsweredFromCache:
		return true
	}
	return false
}
