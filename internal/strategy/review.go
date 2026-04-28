package strategy

import (
	"context"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/classify"
)

// ReviewMidExecution is the v1 budget-exhaustion review step. It runs
// at loop exit (after max_turns is hit, or end_turn from the LLM) and
// returns the action the chat loop should take.
//
// Decision logic (per ticket §"Mid-execution review"):
//
//   - TurnsRemaining > 0:                 ReviewProceed (no exhaustion).
//   - TurnsRemaining ≤ 0 AND HasUsableData: ReviewWrapWithPartialData.
//     The default early-stop synthesis path covers this.
//   - TurnsRemaining ≤ 0 AND NOT HasUsableData: ReviewAskToClarify.
//     The loop appends a clarifying-question message instead of
//     synthesising thin/fabricated content.
//
// ReviewExtendBudget is reserved for v2 — when the strategy would let
// the agent justify a larger budget. v1 always returns proceed instead.
//
// ctx is accepted for symmetry with PlanStrategy and v2 LLM-backed
// reviewers; v1 ignores it.
func ReviewMidExecution(ctx context.Context, state TurnState) ReviewDecision {
	_ = ctx // reserved for v2

	if state.TurnsRemaining > 0 {
		return ReviewProceed
	}

	if state.HasUsableData {
		return ReviewWrapWithPartialData
	}

	return ReviewAskToClarify
}

// ClarifyingQuestion returns the fixed-template clarifying question to
// inject when ReviewMidExecution returns ReviewAskToClarify. v1 uses a
// static template parameterised by the user's intent + any reflex /
// grounding context the planner recorded. v2 will swap in an LLM call
// to author a tailored question.
//
// The template names what's missing in plain English. The chat loop
// appends this string as the assistant's final message instead of the
// usual synthesis.
func ClarifyingQuestion(in PlanInput, strat Strategy) string {
	var b strings.Builder

	b.WriteString("I worked on your request for ")
	b.WriteString(fmt.Sprintf("%d turn(s)", strat.MaxTurns))
	b.WriteString(" but didn't gather enough load-bearing data to answer with confidence.\n\n")

	// Name the topic if we have it. The first sentence of the user
	// input is usually the most useful pointer.
	if topic := firstSentence(in.UserInput); topic != "" {
		b.WriteString("You asked: \"")
		b.WriteString(topic)
		b.WriteString("\"\n\n")
	}

	// Suggest one or two narrowing axes based on intent. The choices
	// are intentionally generic — a tailored question is a v2 concern.
	switch in.ScopeTier {
	case classify.TierLarge, classify.TierOpen:
		b.WriteString("Could you narrow the scope? For example:\n")
		b.WriteString("- A specific component, file path, or system to focus on, or\n")
		b.WriteString("- The single most important question you want answered first.\n")
	case classify.TierMedium:
		b.WriteString("Could you give me one more concrete pointer? For example:\n")
		b.WriteString("- An exact term, file, or identifier I should look at, or\n")
		b.WriteString("- The example of the answer shape you expect.\n")
	default:
		b.WriteString("Could you share a bit more context? For example:\n")
		b.WriteString("- What outcome would count as a good answer, or\n")
		b.WriteString("- An example of the data you'd like me to look at.\n")
	}

	if strat.ReflexMatchID != "" {
		b.WriteString("\n(I noticed a hint of \"")
		b.WriteString(strat.ReflexMatchID)
		b.WriteString("\" in your phrasing — let me know if I should run with that interpretation.)")
	}

	return b.String()
}

// firstSentence returns the first sentence of s, capped at 200 chars,
// with no trailing punctuation. Used in ClarifyingQuestion to echo the
// user's ask without repeating the entire turn.
func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Find the first sentence-terminator.
	for i, r := range s {
		if r == '.' || r == '?' || r == '!' || r == '\n' {
			out := strings.TrimSpace(s[:i])
			if len(out) > 200 {
				out = out[:200]
			}
			return out
		}
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
