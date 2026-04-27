package strategy

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/classify"
)

// TestPlanStrategy_TrivialIntent_DirectChain verifies the trivial-tier
// path: Approach=direct_chain, MaxTurns=BudgetTrivial.
func TestPlanStrategy_TrivialIntent_DirectChain(t *testing.T) {
	s := PlanStrategy(context.Background(), PlanInput{
		ScopeTier:        classify.TierTrivial,
		ExecutionPattern: classify.PatternInline,
	})
	if s.Approach != ApproachDirectChain {
		t.Fatalf("approach: got %q, want %q", s.Approach, ApproachDirectChain)
	}
	if s.MaxTurns != BudgetTrivial {
		t.Fatalf("max turns: got %d, want %d", s.MaxTurns, BudgetTrivial)
	}
	if s.Rationale == "" {
		t.Errorf("expected non-empty rationale")
	}
}

// TestPlanStrategy_MediumIntent_DirectChain verifies medium-tier path.
func TestPlanStrategy_MediumIntent_DirectChain(t *testing.T) {
	s := PlanStrategy(context.Background(), PlanInput{
		ScopeTier:        classify.TierMedium,
		ExecutionPattern: classify.PatternInline,
	})
	if s.Approach != ApproachDirectChain {
		t.Fatalf("approach: got %q, want direct_chain", s.Approach)
	}
	if s.MaxTurns != BudgetMedium {
		t.Fatalf("max turns: got %d, want %d", s.MaxTurns, BudgetMedium)
	}
}

// TestPlanStrategy_SmallIntent_DirectChainMediumBudget verifies small
// tier folds into the medium budget per the documented rule.
func TestPlanStrategy_SmallIntent_DirectChainMediumBudget(t *testing.T) {
	s := PlanStrategy(context.Background(), PlanInput{
		ScopeTier:        classify.TierSmall,
		ExecutionPattern: classify.PatternInline,
	})
	if s.Approach != ApproachDirectChain {
		t.Fatalf("approach: got %q, want direct_chain", s.Approach)
	}
	if s.MaxTurns != BudgetMedium {
		t.Fatalf("max turns: got %d, want %d (small folds to medium)", s.MaxTurns, BudgetMedium)
	}
}

// TestPlanStrategy_LargeIntent_SubagentDelegation verifies large/open
// tiers route to subagent_delegation.
func TestPlanStrategy_LargeIntent_SubagentDelegation(t *testing.T) {
	s := PlanStrategy(context.Background(), PlanInput{
		ScopeTier:        classify.TierLarge,
		ExecutionPattern: classify.PatternSubagent,
	})
	if s.Approach != ApproachSubagentDelegation {
		t.Fatalf("approach: got %q, want subagent_delegation", s.Approach)
	}
	if s.MaxTurns != BudgetLarge {
		t.Fatalf("max turns: got %d, want %d", s.MaxTurns, BudgetLarge)
	}
}

// TestPlanStrategy_OpenIntent_SubagentDelegation verifies open tier.
func TestPlanStrategy_OpenIntent_SubagentDelegation(t *testing.T) {
	s := PlanStrategy(context.Background(), PlanInput{
		ScopeTier:        classify.TierOpen,
		ExecutionPattern: classify.PatternSubagent,
	})
	if s.Approach != ApproachSubagentDelegation {
		t.Fatalf("approach: got %q, want subagent_delegation", s.Approach)
	}
	if s.MaxTurns != BudgetLarge {
		t.Fatalf("max turns: got %d, want %d", s.MaxTurns, BudgetLarge)
	}
}

// TestPlanStrategy_BackgroundReflex_OverridesIntent verifies a reflex
// match with PatternBackground forces subagent_delegation regardless of
// tier classification.
func TestPlanStrategy_BackgroundReflex_OverridesIntent(t *testing.T) {
	s := PlanStrategy(context.Background(), PlanInput{
		ScopeTier:        classify.TierTrivial,
		ExecutionPattern: classify.PatternInline,
		ReflexMatch: &ReflexSignal{
			ReflexID:    "background-long-task",
			HintPattern: classify.PatternBackground,
			DispatchVia: "executeBackground",
		},
	})
	if s.Approach != ApproachSubagentDelegation {
		t.Fatalf("approach: got %q, want subagent_delegation (background reflex)", s.Approach)
	}
	if s.MaxTurns != BudgetLarge {
		t.Fatalf("max turns: got %d, want %d", s.MaxTurns, BudgetLarge)
	}
	if s.ReflexMatchID != "background-long-task" {
		t.Errorf("reflex match id: got %q, want background-long-task", s.ReflexMatchID)
	}
}

// TestPlanStrategy_GroundingPriorSuccess_AdoptsApproach verifies the
// grounding-driven prior-success rule: when a recalled memory says a
// previous similar ask succeeded with subagent_delegation, the planner
// adopts it even on a TierMedium intent.
func TestPlanStrategy_GroundingPriorSuccess_AdoptsApproach(t *testing.T) {
	s := PlanStrategy(context.Background(), PlanInput{
		ScopeTier:        classify.TierMedium,
		ExecutionPattern: classify.PatternInline,
		Grounding: &GroundingSignal{
			Enabled:              true,
			ConsultationIDs:      []int64{42, 43},
			PriorSuccessApproach: ApproachSubagentDelegation,
		},
	})
	if s.Approach != ApproachSubagentDelegation {
		t.Fatalf("approach: got %q, want subagent_delegation (grounding adoption)", s.Approach)
	}
	if s.MaxTurns != BudgetLarge {
		t.Fatalf("max turns: got %d, want %d", s.MaxTurns, BudgetLarge)
	}
	if got := s.GroundingConsultationIDs; len(got) != 2 || got[0] != 42 || got[1] != 43 {
		t.Errorf("grounding ids: got %v, want [42 43]", got)
	}
}

// TestPlanStrategy_GroundingTersePreference_RecordedInRationale
// verifies the prefers-terse signal is folded into the rationale text
// without changing the budget.
func TestPlanStrategy_GroundingTersePreference_RecordedInRationale(t *testing.T) {
	s := PlanStrategy(context.Background(), PlanInput{
		ScopeTier:        classify.TierMedium,
		ExecutionPattern: classify.PatternInline,
		Grounding: &GroundingSignal{
			Enabled:      true,
			PrefersTerse: true,
		},
	})
	if s.Approach != ApproachDirectChain {
		t.Fatalf("approach: got %q, want direct_chain", s.Approach)
	}
	// rationale must mention the terse preference somewhere.
	if !contains(s.Rationale, "terse") {
		t.Errorf("expected rationale to mention 'terse', got %q", s.Rationale)
	}
}

// TestPlanStrategy_NoSignals_DefaultDirectChain verifies the default
// fallback when neither tier nor pattern is set: direct_chain with
// medium budget.
func TestPlanStrategy_NoSignals_DefaultDirectChain(t *testing.T) {
	s := PlanStrategy(context.Background(), PlanInput{})
	if s.Approach != ApproachDirectChain {
		t.Fatalf("approach: got %q, want direct_chain (default)", s.Approach)
	}
	if s.MaxTurns != DefaultBudget {
		t.Fatalf("max turns: got %d, want %d (default)", s.MaxTurns, DefaultBudget)
	}
	if s.MaxTurns != BudgetMedium {
		t.Errorf("default budget should equal BudgetMedium")
	}
}

// TestPlanStrategy_AdvisoryReflex_RecordedInRationale verifies a
// non-overriding reflex still gets recorded so analytics can correlate.
func TestPlanStrategy_AdvisoryReflex_RecordedInRationale(t *testing.T) {
	s := PlanStrategy(context.Background(), PlanInput{
		ScopeTier:        classify.TierMedium,
		ExecutionPattern: classify.PatternInline,
		ReflexMatch: &ReflexSignal{
			ReflexID:    "documentor-mention",
			HintPattern: classify.PatternSubagent,
		},
	})
	if s.ReflexMatchID != "documentor-mention" {
		t.Errorf("reflex match id: got %q, want documentor-mention", s.ReflexMatchID)
	}
	if !contains(s.Rationale, "documentor-mention") {
		t.Errorf("rationale missing reflex id: %q", s.Rationale)
	}
}

// TestPlanStrategy_GroundingDisabled_TreatedAsAbsent verifies a
// GroundingSignal with Enabled=false is ignored entirely.
func TestPlanStrategy_GroundingDisabled_TreatedAsAbsent(t *testing.T) {
	s := PlanStrategy(context.Background(), PlanInput{
		ScopeTier: classify.TierTrivial,
		Grounding: &GroundingSignal{
			Enabled:              false,
			PriorSuccessApproach: ApproachSubagentDelegation,
		},
	})
	// PriorSuccessApproach must not apply because grounding is disabled.
	if s.Approach != ApproachDirectChain {
		t.Errorf("approach: got %q, want direct_chain (grounding disabled)", s.Approach)
	}
	if s.MaxTurns != BudgetTrivial {
		t.Errorf("max turns: got %d, want %d", s.MaxTurns, BudgetTrivial)
	}
}

func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
