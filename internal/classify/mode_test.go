package classify

import (
	"slices"
	"testing"
)

func TestClassifyMode(t *testing.T) {
	cases := []struct {
		name           string
		input          string
		wantSuggested  string
		minConfidence  float64
		maxConfidence  float64 // 0 = unbounded
		mustContainSig ModeSignal
	}{
		// ---- Slash prefix (rule 1) ----
		{name: "slash chat", input: "/chat what's up", wantSuggested: "chat", minConfidence: 1.0, maxConfidence: 1.0, mustContainSig: SignalSlashChat},
		{name: "slash plan", input: "/plan a launch", wantSuggested: "plan", minConfidence: 1.0, maxConfidence: 1.0, mustContainSig: SignalSlashPlan},
		{name: "slash work", input: "/work on the API", wantSuggested: "work", minConfidence: 1.0, maxConfidence: 1.0, mustContainSig: SignalSlashWork},

		// ---- Imperative-plan phrases (rule 2) ----
		{name: "help me plan a launch", input: "help me plan a launch", wantSuggested: "plan", minConfidence: 0.8, mustContainSig: SignalImperativePlan},
		{name: "let's plan dinner", input: "let's plan dinner", wantSuggested: "plan", minConfidence: 0.8, mustContainSig: SignalImperativePlan},
		{name: "draft an outline", input: "Draft an outline of the proposal", wantSuggested: "plan", minConfidence: 0.8, mustContainSig: SignalImperativePlan},
		{name: "outline the steps", input: "Please outline the migration steps", wantSuggested: "plan", minConfidence: 0.8, mustContainSig: SignalImperativePlan},
		{name: "brainstorm ideas", input: "Let's brainstorm ideas for the launch", wantSuggested: "plan", minConfidence: 0.8, mustContainSig: SignalImperativePlan},
		{name: "design the schema", input: "Design the schema for orders", wantSuggested: "plan", minConfidence: 0.8, mustContainSig: SignalImperativePlan},
		{name: "schedule a meeting", input: "Can you schedule a meeting for tomorrow", wantSuggested: "plan", minConfidence: 0.8, mustContainSig: SignalImperativePlan},

		// ---- Imperative-work phrases (rule 2) ----
		{name: "let's implement", input: "let's implement the parser", wantSuggested: "work", minConfidence: 0.8, mustContainSig: SignalImperativeWork},
		{name: "let's build", input: "Let's build the chip component", wantSuggested: "work", minConfidence: 0.8, mustContainSig: SignalImperativeWork},
		{name: "let's ship", input: "Let's ship it!", wantSuggested: "work", minConfidence: 0.8, mustContainSig: SignalImperativeWork},
		{name: "help me fix", input: "Help me fix this bug", wantSuggested: "work", minConfidence: 0.8, mustContainSig: SignalImperativeWork},
		{name: "go ahead and merge", input: "Go ahead and merge the PR", wantSuggested: "work", minConfidence: 0.8, mustContainSig: SignalImperativeWork},
		{name: "please implement", input: "Please implement the validator", wantSuggested: "work", minConfidence: 0.8, mustContainSig: SignalImperativeWork},

		// ---- Action verbs (rule 3) ----
		{name: "implement first word", input: "implement the cache layer", wantSuggested: "work", minConfidence: 0.65, maxConfidence: 0.75, mustContainSig: SignalActionVerbWork},
		{name: "fix first word", input: "fix the typo in README", wantSuggested: "work", minConfidence: 0.65, maxConfidence: 0.75, mustContainSig: SignalActionVerbWork},
		{name: "refactor first word", input: "Refactor the auth module", wantSuggested: "work", minConfidence: 0.65, maxConfidence: 0.75, mustContainSig: SignalActionVerbWork},
		{name: "deploy first word", input: "deploy to staging", wantSuggested: "work", minConfidence: 0.65, maxConfidence: 0.75, mustContainSig: SignalActionVerbWork},
		{name: "rename first word with comma", input: "rename, please, the variable", wantSuggested: "work", minConfidence: 0.65, maxConfidence: 0.75, mustContainSig: SignalActionVerbWork},

		// ---- Default chat (rule 4) ----
		{name: "question default chat", input: "What is React?", wantSuggested: "chat", minConfidence: 0.0, maxConfidence: 0.0, mustContainSig: SignalDefaultChat},
		{name: "rambling default chat", input: "I was wondering about the ocean tides today", wantSuggested: "chat", minConfidence: 0.0, maxConfidence: 0.0, mustContainSig: SignalDefaultChat},

		// ---- Edge cases ----
		{name: "empty string", input: "", wantSuggested: "", minConfidence: 0.0, maxConfidence: 0.0},
		{name: "whitespace only", input: "   \n\t  ", wantSuggested: "", minConfidence: 0.0, maxConfidence: 0.0},

		// Word-boundary safety: "planet" and "planets" must NOT trigger plan.
		{name: "planet not plan", input: "planet vs space", wantSuggested: "chat", minConfidence: 0.0, maxConfidence: 0.0, mustContainSig: SignalDefaultChat},
		{name: "planets default chat", input: "planets are big", wantSuggested: "chat", minConfidence: 0.0, maxConfidence: 0.0, mustContainSig: SignalDefaultChat},

		// Mixed-case safety: "LET'S PLAN A trip" still triggers plan.
		{name: "uppercase let's plan", input: "LET'S PLAN A trip", wantSuggested: "plan", minConfidence: 0.8, mustContainSig: SignalImperativePlan},
		{name: "mixed case help me plan", input: "HeLp Me PlAn A LaUnCh", wantSuggested: "plan", minConfidence: 0.8, mustContainSig: SignalImperativePlan},

		// Slash prefix wins over imperative phrase even when both present.
		{name: "slash work beats action verb", input: "/work implement the cache", wantSuggested: "work", minConfidence: 1.0, maxConfidence: 1.0, mustContainSig: SignalSlashWork},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyMode(tc.input)
			if got.Suggested != tc.wantSuggested {
				t.Fatalf("Suggested mismatch: got %q, want %q (input=%q, full=%+v)",
					got.Suggested, tc.wantSuggested, tc.input, got)
			}
			if got.Confidence < tc.minConfidence {
				t.Fatalf("Confidence %.2f < min %.2f (input=%q, full=%+v)",
					got.Confidence, tc.minConfidence, tc.input, got)
			}
			if tc.maxConfidence > 0 && got.Confidence > tc.maxConfidence {
				t.Fatalf("Confidence %.2f > max %.2f (input=%q, full=%+v)",
					got.Confidence, tc.maxConfidence, tc.input, got)
			}
			if tc.mustContainSig != "" {
				if !slices.Contains(got.Signals, tc.mustContainSig) {
					t.Fatalf("Signals missing %q: got %v (input=%q)",
						tc.mustContainSig, got.Signals, tc.input)
				}
			}
			if tc.wantSuggested == "" && len(got.Signals) != 0 {
				t.Fatalf("expected empty Signals for empty input, got %v", got.Signals)
			}
		})
	}
}

func TestPhraseHitWordBoundary(t *testing.T) {
	cases := []struct {
		haystack string
		phrase   string
		want     bool
	}{
		{"plan a launch", "plan a", true},
		{"i plan a launch", "plan a", true},
		{"planet a", "plan a", false}, // 'plan' followed by 'e' — not a boundary
		{"planning", "plan", false},   // 'plan' followed by 'n'
		{"the plan, then", "plan", true},
		{"unplanned", "plan", false}, // 'plan' preceded by 'n'
		{"", "plan", false},
		{"plan", "plan", true},
	}
	for _, tc := range cases {
		got := phraseHit(tc.haystack, tc.phrase)
		if got != tc.want {
			t.Errorf("phraseHit(%q, %q) = %v, want %v", tc.haystack, tc.phrase, got, tc.want)
		}
	}
}
