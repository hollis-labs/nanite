package strategy

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/classify"
)

// TestReviewMidExecution_TurnsRemaining_Proceed verifies the loop is
// allowed to keep running when budget is not exhausted.
func TestReviewMidExecution_TurnsRemaining_Proceed(t *testing.T) {
	d := ReviewMidExecution(context.Background(), TurnState{
		TurnsConsumed:  3,
		TurnsRemaining: 5,
		HasUsableData:  false,
	})
	if d != ReviewProceed {
		t.Fatalf("decision: got %q, want %q", d, ReviewProceed)
	}
}

// TestReviewMidExecution_TurnsRemaining_ProceedWithUsableData verifies
// proceed beats wrap when there is still budget AND data.
func TestReviewMidExecution_TurnsRemaining_ProceedWithUsableData(t *testing.T) {
	d := ReviewMidExecution(context.Background(), TurnState{
		TurnsConsumed:  3,
		TurnsRemaining: 5,
		HasUsableData:  true,
	})
	if d != ReviewProceed {
		t.Fatalf("decision: got %q, want %q", d, ReviewProceed)
	}
}

// TestReviewMidExecution_BudgetExhausted_NoData_AskToClarify verifies
// the load-bearing budget-exhaustion + no-data branch.
func TestReviewMidExecution_BudgetExhausted_NoData_AskToClarify(t *testing.T) {
	d := ReviewMidExecution(context.Background(), TurnState{
		TurnsConsumed:  10,
		TurnsRemaining: 0,
		HasUsableData:  false,
	})
	if d != ReviewAskToClarify {
		t.Fatalf("decision: got %q, want %q", d, ReviewAskToClarify)
	}
}

// TestReviewMidExecution_BudgetExhausted_HasData_WrapPartial verifies
// the wrap-with-partial-data path.
func TestReviewMidExecution_BudgetExhausted_HasData_WrapPartial(t *testing.T) {
	d := ReviewMidExecution(context.Background(), TurnState{
		TurnsConsumed:  10,
		TurnsRemaining: 0,
		HasUsableData:  true,
	})
	if d != ReviewWrapWithPartialData {
		t.Fatalf("decision: got %q, want %q", d, ReviewWrapWithPartialData)
	}
}

// TestReviewMidExecution_OverBudget_NoData_AskToClarify verifies
// negative TurnsRemaining (over budget) still routes to ask_to_clarify
// when no usable data was produced.
func TestReviewMidExecution_OverBudget_NoData_AskToClarify(t *testing.T) {
	d := ReviewMidExecution(context.Background(), TurnState{
		TurnsConsumed:  12,
		TurnsRemaining: -2,
		HasUsableData:  false,
	})
	if d != ReviewAskToClarify {
		t.Fatalf("decision: got %q, want %q", d, ReviewAskToClarify)
	}
}

// TestClarifyingQuestion_NamesBudgetAndTopic verifies the v1 fixed
// template echoes the budget hit and the user's topic.
func TestClarifyingQuestion_NamesBudgetAndTopic(t *testing.T) {
	q := ClarifyingQuestion(
		PlanInput{
			UserInput: "Find every config file in the repo and explain each one. Then build a graph.",
			ScopeTier: classify.TierLarge,
		},
		Strategy{
			MaxTurns:      40,
			ReflexMatchID: "researcher-mention",
		},
	)
	if !strings.Contains(q, "40") {
		t.Errorf("clarifying question missing budget: %q", q)
	}
	if !strings.Contains(q, "Find every config file in the repo and explain each one") {
		t.Errorf("clarifying question missing topic echo: %q", q)
	}
	if !strings.Contains(q, "researcher-mention") {
		t.Errorf("clarifying question missing reflex hint: %q", q)
	}
	if !strings.Contains(q, "narrow") {
		t.Errorf("clarifying question missing narrow-the-scope prompt for TierLarge: %q", q)
	}
}

// TestClarifyingQuestion_TrivialIntent_GenericPrompt verifies trivial
// scope produces a generic narrowing question, not a "narrow scope"
// pitch (which only fits large/open).
func TestClarifyingQuestion_TrivialIntent_GenericPrompt(t *testing.T) {
	q := ClarifyingQuestion(
		PlanInput{
			UserInput: "what time is it",
			ScopeTier: classify.TierTrivial,
		},
		Strategy{MaxTurns: 10},
	)
	if strings.Contains(q, "narrow the scope") {
		t.Errorf("trivial-tier clarification should not pitch scope-narrowing: %q", q)
	}
	if !strings.Contains(q, "10 turn") {
		t.Errorf("clarifying question missing budget: %q", q)
	}
}

// TestFirstSentence covers the helper used by ClarifyingQuestion. The
// ClarifyingQuestion test above already exercises the happy path; this
// pins edge cases (empty input, no terminator, terminator at end).
func TestFirstSentence(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"hello world", "hello world"},
		{"hello world.", "hello world"},
		{"first one. second one.", "first one"},
		{"only?", "only"},
	}
	for _, c := range cases {
		if got := firstSentence(c.in); got != c.want {
			t.Errorf("firstSentence(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
