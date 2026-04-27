package grounding_test

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/grounding"
)

func TestClassifyOutcome_EmptyFollowUp(t *testing.T) {
	o := grounding.ClassifyOutcome("", 0)
	if o.Kind != grounding.OutcomeUnknown {
		t.Errorf("empty follow-up: want OutcomeUnknown, got %q", o.Kind)
	}
}

func TestClassifyOutcome_WhitespaceFollowUp(t *testing.T) {
	o := grounding.ClassifyOutcome("   \t\n  ", 0)
	if o.Kind != grounding.OutcomeUnknown {
		t.Errorf("whitespace follow-up: want OutcomeUnknown, got %q", o.Kind)
	}
}

func TestClassifyOutcome_PruningWords(t *testing.T) {
	cases := []struct {
		followUp      string
		wantKind      grounding.OutcomeKind
		wantPruneWord string
	}{
		{"just the title please", grounding.OutcomeRefined, "just"},
		{"only show me the summary", grounding.OutcomeRefined, "only"},
		{"can you be more narrower", grounding.OutcomeRefined, "narrower"},
		{"more specific answer needed", grounding.OutcomeRefined, "more specific"},
		{"that was too broad for me", grounding.OutcomeRefined, "too broad"},
		{"the response was too much", grounding.OutcomeRefined, "too much"},
		{"focus on the backend", grounding.OutcomeRefined, "focus on"},
	}

	for _, tc := range cases {
		t.Run(tc.followUp, func(t *testing.T) {
			o := grounding.ClassifyOutcome(tc.followUp, 0)
			if o.Kind != tc.wantKind {
				t.Errorf("want %q, got %q", tc.wantKind, o.Kind)
			}
			found := false
			for _, w := range o.PruningWordsFound {
				if w == tc.wantPruneWord {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected pruning word %q in %v", tc.wantPruneWord, o.PruningWordsFound)
			}
		})
	}
}

func TestClassifyOutcome_Accepted_LongFollowUp(t *testing.T) {
	// A long follow-up with no pruning words and no quick timing → accepted.
	long := "That was great, thank you. Can you now tell me more about the architecture " +
		"decisions made in the early stages of the project, specifically around the " +
		"storage layer and why SQLite was chosen over PostgreSQL for the embedded case?"
	o := grounding.ClassifyOutcome(long, 300) // 5 minutes later — not quick
	if o.Kind != grounding.OutcomeAccepted {
		t.Errorf("long accepted follow-up: want OutcomeAccepted, got %q", o.Kind)
	}
	if len(o.PruningWordsFound) != 0 {
		t.Errorf("expected no pruning words, got %v", o.PruningWordsFound)
	}
}

func TestClassifyOutcome_QuickTerseRefinement(t *testing.T) {
	// Short follow-up within the refinement window → refined (no pruning words).
	short := "make it shorter"
	o := grounding.ClassifyOutcome(short, 30) // 30 seconds — within window
	if o.Kind != grounding.OutcomeRefined {
		t.Errorf("quick terse refinement: want OutcomeRefined, got %q", o.Kind)
	}
}

func TestClassifyOutcome_ShortFollowUp_OutsideWindow(t *testing.T) {
	// Short follow-up but outside the timing window, no pruning words.
	// The "narrower" path: heuristic uses timing window only — outside window
	// AND no pruning words → accepted.
	short := "tell me about dogs"
	o := grounding.ClassifyOutcome(short, 200) // 200 seconds, outside RefinementWindowSeconds=90
	if o.Kind != grounding.OutcomeAccepted {
		t.Errorf("short outside window: want OutcomeAccepted, got %q", o.Kind)
	}
}

func TestClassifyOutcome_ExcerptTruncated(t *testing.T) {
	// Very long follow-up — excerpt should be capped at 200 chars.
	long := make([]byte, 400)
	for i := range long {
		long[i] = 'x'
	}
	o := grounding.ClassifyOutcome(string(long), 0)
	if len(o.FollowUpExcerpt) > 200 {
		t.Errorf("excerpt too long: %d chars", len(o.FollowUpExcerpt))
	}
}

func TestClassifyOutcome_SecondsSinceAckPreserved(t *testing.T) {
	o := grounding.ClassifyOutcome("just a quick one", 42.5)
	if o.SecondsSinceAck != 42.5 {
		t.Errorf("secondsSinceAck: want 42.5, got %v", o.SecondsSinceAck)
	}
}
