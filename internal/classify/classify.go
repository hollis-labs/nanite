package classify

import "strings"

// ScopeTierOpenKeywords, etc. are exported so the Arc 7 eval suite
// (CW-20260420-0028) can tune the rubric without rewriting the classifier.
// Keyword matching is case-insensitive and substring-based.
var (
	ScopeTierOpenKeywords = []string{
		"build", "scaffold", "create a complete", "make an app",
		"full implementation", "end-to-end", "research deeply", "exhaustive",
		"full migration", "full research",
	}
	ScopeTierLargeKeywords = []string{
		"refactor", "migrate", "rewrite", "restructure",
		"port to", "convert all", "across the codebase",
	}
	ScopeTierMediumKeywords = []string{
		"implement", "add feature", "wire up", "integrate", "fix and test",
	}
	ScopeTierSmallKeywords = []string{
		"fix", "change", "update", "rename", "what does", "where is",
	}
)

var (
	ExecutionPatternBackgroundKeywords = []string{
		"in the background", "kick off", "let me know when", "poll",
		"run overnight", "detach", "send to inbox",
	}
	ExecutionPatternSubagentKeywords = []string{
		"research", "investigate", "audit", "survey", "dispatch to",
	}
	ExecutionPatternBackgroundFollowUpKeywords = []string{
		"while i", "i'll check back",
	}
)

// Classify maps an intent signal to a (ScopeTier, ExecutionPattern) pair.
// Rules-based MVP per D5. See internal/classify/doc.go for the consumer
// contract and the rubric's stability guarantees.
//
// Conservative-default: ambiguous or empty input returns (TierSmall, PatternInline).
func Classify(intent IntentSignals) (ScopeTier, ExecutionPattern) {
	tier := classifyTier(intent)
	pattern := classifyPattern(intent, tier)
	return tier, pattern
}

func classifyTier(intent IntentSignals) ScopeTier {
	msg := strings.ToLower(intent.Message)
	est := intent.MessageTokenEst

	// Very short inputs are trivial regardless of keywords — a 3-4 token
	// message like "poll the build" or "audit logs" is trivial even if it
	// contains a tier-escalating word.
	if est > 0 && est <= 10 {
		return TierTrivial
	}

	// Keyword-driven tiers (highest-intensity first).
	if containsAny(msg, ScopeTierOpenKeywords) {
		return TierOpen
	}
	if containsAny(msg, ScopeTierLargeKeywords) {
		return TierLarge
	}
	if containsAny(msg, ScopeTierMediumKeywords) {
		return TierMedium
	}

	// Length-driven fallback (only reached if no tier-escalating keyword matched).
	switch {
	case est > 2000:
		return TierOpen
	case est > 800:
		return TierLarge
	case est > 200:
		return TierMedium
	case est <= 30 && est > 0:
		// TierTrivial requires explicitly short input; guards zero-value fallthrough.
		if !containsAny(msg, ScopeTierSmallKeywords) {
			return TierTrivial
		}
	}

	// Conservative default.
	return TierSmall
}

func classifyPattern(intent IntentSignals, tier ScopeTier) ExecutionPattern {
	msg := strings.ToLower(intent.Message)

	// Background first — explicit wins regardless of tier.
	if containsAny(msg, ExecutionPatternBackgroundKeywords) {
		return PatternBackground
	}
	if tier == TierOpen && containsAny(msg, ExecutionPatternBackgroundFollowUpKeywords) {
		return PatternBackground
	}

	// Subagent — size-driven OR keyword-driven.
	if tier == TierLarge || tier == TierOpen {
		return PatternSubagent
	}
	if containsAny(msg, ExecutionPatternSubagentKeywords) {
		return PatternSubagent
	}

	// Default.
	return PatternInline
}

func containsAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}
