package classify

import "testing"

// TestClassify covers every reachable ScopeTier × ExecutionPattern combo,
// plus the conservative-default fallback and a few edge cases.
// TierLarge × PatternInline and TierOpen × PatternInline are unreachable
// by the MVP rubric (size-driven Subagent escalation); see doc.go.
func TestClassify(t *testing.T) {
	cases := []struct {
		name        string
		signals     IntentSignals
		wantTier    ScopeTier
		wantPattern ExecutionPattern
	}{
		// --- TierTrivial ---
		{
			name:        "trivial inline — short Q&A",
			signals:     IntentSignals{Message: "what is 2 + 2?", MessageTokenEst: 5},
			wantTier:    TierTrivial,
			wantPattern: PatternInline,
		},
		{
			name:        "trivial inline — yes/no",
			signals:     IntentSignals{Message: "is main.go committed?", MessageTokenEst: 6},
			wantTier:    TierTrivial,
			wantPattern: PatternInline,
		},

		// --- TierSmall ---
		{
			name:        "small inline — simple edit",
			signals:     IntentSignals{Message: "fix the typo in README.md line 12", MessageTokenEst: 60},
			wantTier:    TierSmall,
			wantPattern: PatternInline,
		},
		{
			name:        "small inline — default fallback for ambiguous short input",
			signals:     IntentSignals{Message: "help with this", MessageTokenEst: 80},
			wantTier:    TierSmall,
			wantPattern: PatternInline,
		},

		// --- TierMedium ---
		{
			name:        "medium inline — implement",
			signals:     IntentSignals{Message: "implement a helper to parse the YAML front-matter and add a test", MessageTokenEst: 300},
			wantTier:    TierMedium,
			wantPattern: PatternInline,
		},
		{
			name:        "medium subagent — investigate",
			signals:     IntentSignals{Message: "investigate why the export is missing rows", MessageTokenEst: 250},
			wantTier:    TierMedium,
			wantPattern: PatternSubagent,
		},

		// --- TierLarge ---
		{
			name:        "large subagent — refactor",
			signals:     IntentSignals{Message: "refactor the notifications module to decouple the provider adapter from the queue", MessageTokenEst: 900},
			wantTier:    TierLarge,
			wantPattern: PatternSubagent,
		},
		{
			name:        "large subagent — size-driven",
			signals:     IntentSignals{Message: "rewrite this function; " + longString(900), MessageTokenEst: 1100},
			wantTier:    TierLarge,
			wantPattern: PatternSubagent,
		},

		// --- TierOpen ---
		{
			name:        "open subagent — build something",
			signals:     IntentSignals{Message: "build a complete CRUD app for tracking books with auth and tests", MessageTokenEst: 2500},
			wantTier:    TierOpen,
			wantPattern: PatternSubagent,
		},
		{
			name:        "open background — explicit background keyword",
			signals:     IntentSignals{Message: "kick off a full research sweep on the topic and send to inbox when done", MessageTokenEst: 2200},
			wantTier:    TierOpen,
			wantPattern: PatternBackground,
		},
		{
			name:        "open background — I'll check back pattern",
			signals:     IntentSignals{Message: "build the full migration pipeline end-to-end while I work on the UI; I'll check back in a bit", MessageTokenEst: 2800},
			wantTier:    TierOpen,
			wantPattern: PatternBackground,
		},

		// --- Additional combos for tier × pattern matrix coverage ---
		{
			name:        "trivial subagent — very short + subagent keyword",
			signals:     IntentSignals{Message: "audit logs", MessageTokenEst: 3},
			wantTier:    TierTrivial,
			wantPattern: PatternSubagent,
		},
		{
			name:        "trivial background — very short + background keyword",
			signals:     IntentSignals{Message: "poll the build", MessageTokenEst: 4},
			wantTier:    TierTrivial,
			wantPattern: PatternBackground,
		},
		{
			name:        "small subagent — short + subagent keyword",
			signals:     IntentSignals{Message: "investigate the failure in the log line", MessageTokenEst: 50},
			wantTier:    TierSmall,
			wantPattern: PatternSubagent,
		},
		{
			name:        "medium background — implement + background keyword",
			signals:     IntentSignals{Message: "implement the pagination helper in the background; send to inbox when done", MessageTokenEst: 350},
			wantTier:    TierMedium,
			wantPattern: PatternBackground,
		},
		{
			name:        "large background — refactor + background keyword",
			signals:     IntentSignals{Message: "refactor the notifications module in the background and let me know when ready", MessageTokenEst: 900},
			wantTier:    TierLarge,
			wantPattern: PatternBackground,
		},

		// --- Edge / fallback ---
		{
			name:        "empty input falls back to (small, inline)",
			signals:     IntentSignals{Message: "", MessageTokenEst: 0},
			wantTier:    TierSmall,
			wantPattern: PatternInline,
		},
		{
			name:        "background keyword at low tier still routes background",
			signals:     IntentSignals{Message: "kick off a background audit of the logs", MessageTokenEst: 40},
			wantTier:    TierSmall,
			wantPattern: PatternBackground,
		},
		{
			name:        "attachments do not escalate tier alone",
			signals:     IntentSignals{Message: "summarize this", MessageTokenEst: 15, HasAttachments: true},
			wantTier:    TierTrivial,
			wantPattern: PatternInline,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tier, pattern := Classify(c.signals)
			if tier != c.wantTier {
				t.Errorf("tier = %v (%s), want %v (%s)", tier, tier, c.wantTier, c.wantTier)
			}
			if pattern != c.wantPattern {
				t.Errorf("pattern = %v (%s), want %v (%s)", pattern, pattern, c.wantPattern, c.wantPattern)
			}
		})
	}
}

// TestClassify_NeverReturnsInvalid guards against future rule changes producing
// an out-of-range value. Property-style smoke check over 5 seed inputs.
func TestClassify_NeverReturnsInvalid(t *testing.T) {
	seeds := []IntentSignals{
		{},
		{Message: "hi"},
		{Message: "refactor everything", MessageTokenEst: 10000},
		{Message: "   ", MessageTokenEst: -1},
		{Message: "kick off a background massive overnight build", MessageTokenEst: 5000},
	}
	for i, s := range seeds {
		tier, pattern := Classify(s)
		if !tier.IsValid() {
			t.Errorf("seed %d: tier %v invalid", i, tier)
		}
		if !pattern.IsValid() {
			t.Errorf("seed %d: pattern %v invalid", i, pattern)
		}
	}
}

// TestInvalidSentinels_StringOutput locks in the "invalid" string for the
// iota-0 sentinels so downstream slog lines render consistently when
// Classify has not yet been called. Addresses Task 2 code-review nit.
func TestInvalidSentinels_StringOutput(t *testing.T) {
	if got := TierInvalid.String(); got != "invalid" {
		t.Errorf("TierInvalid.String() = %q, want %q", got, "invalid")
	}
	if got := ScopeTier(99).String(); got != "invalid" {
		t.Errorf("ScopeTier(99).String() = %q, want %q", got, "invalid")
	}
	if got := PatternInvalid.String(); got != "invalid" {
		t.Errorf("PatternInvalid.String() = %q, want %q", got, "invalid")
	}
	if got := ExecutionPattern(99).String(); got != "invalid" {
		t.Errorf("ExecutionPattern(99).String() = %q, want %q", got, "invalid")
	}
}

func longString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}
