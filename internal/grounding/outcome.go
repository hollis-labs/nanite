package grounding

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// pruningPhrases are the trigger words/phrases that indicate the user is
// narrowing or re-scoping their request. A follow-up containing any of these
// is classified as OutcomeRefined.
//
// Phrases are lower-case; matching is done on the normalized follow-up text.
// Order does not matter — all matches are collected for the log.
var pruningPhrases = []string{
	"just ",
	"only ",
	"narrower",
	"more specific",
	"more specifically",
	"be more specific",
	"simpler",
	"shorter",
	"focus on",
	"limit to",
	"restrict to",
	"can you just",
	"can you only",
	"actually just",
	"actually only",
	"not all of",
	"not everything",
	"too broad",
	"too much",
}

// RefinementWindowSeconds is the maximum gap between the assistant ack and a
// user follow-up for the turn to be considered a quick refinement even
// without explicit pruning words. Tight turns that arrive within this window
// and look like reformulations signal OutcomeRefined.
const RefinementWindowSeconds = 90

// ClassifyOutcome applies the next-turn refinement heuristic to determine
// whether the user's follow-up indicates acceptance or narrowing.
//
// It returns OutcomeRefined when:
//   - The follow-up text contains any pruning phrase, OR
//   - The follow-up arrived within RefinementWindowSeconds and the text is
//     short (< 120 chars, suggesting a terse re-ask rather than a new topic).
//
// It returns OutcomeAccepted when neither condition is met.
// It returns OutcomeUnknown when followUpText is empty (no follow-up observed).
//
// secondsSinceAck is the gap between assistant response and the follow-up
// user turn; pass 0 when unknown.
func ClassifyOutcome(followUpText string, secondsSinceAck float64) Outcome {
	if strings.TrimSpace(followUpText) == "" {
		return Outcome{Kind: OutcomeUnknown}
	}

	norm := strings.ToLower(followUpText)
	var found []string
	for _, p := range pruningPhrases {
		if strings.Contains(norm, p) {
			found = append(found, strings.TrimSpace(p))
		}
	}

	excerpt := followUpText
	if len(excerpt) > 200 {
		excerpt = excerpt[:200]
	}

	if len(found) > 0 {
		return Outcome{
			Kind:              OutcomeRefined,
			FollowUpExcerpt:   excerpt,
			SecondsSinceAck:   secondsSinceAck,
			PruningWordsFound: found,
		}
	}

	// Quick terse follow-up within the refinement window: treat as refined
	// even without explicit pruning words.
	if secondsSinceAck > 0 && secondsSinceAck <= RefinementWindowSeconds && len(followUpText) < 120 {
		return Outcome{
			Kind:            OutcomeRefined,
			FollowUpExcerpt: excerpt,
			SecondsSinceAck: secondsSinceAck,
		}
	}

	return Outcome{
		Kind:            OutcomeAccepted,
		FollowUpExcerpt: excerpt,
		SecondsSinceAck: secondsSinceAck,
	}
}

// RecordOutcome writes the heuristic outcome for all consultation rows that
// were surfaced in the grounding result. consultationIDs comes from
// LogConsultations (surfaced-only row IDs). If no IDs are provided the call
// is a no-op.
//
// ackTime is the wall-clock time the assistant finished its response; it is
// used to compute secondsSinceAck. followUpText is the raw next-turn user
// input. logger may be nil (outcome logging is always best-effort).
func RecordOutcome(
	logger ConsultationLogger,
	consultationIDs []int64,
	followUpText string,
	ackTime time.Time,
) {
	if logger == nil || len(consultationIDs) == 0 || strings.TrimSpace(followUpText) == "" {
		return
	}

	var secs float64
	if !ackTime.IsZero() {
		secs = time.Since(ackTime).Seconds()
	}

	outcome := ClassifyOutcome(followUpText, secs)

	for _, id := range consultationIDs {
		o := outcome
		o.ConsultationID = id
		if err := logger.LogGroundingOutcome(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, o); err != nil {
			slog.Warn("grounding: log outcome error (non-fatal)", "err", err, "consultation_id", id)
		}
	}
}
