package promptrouter

import (
	"strings"
	"unicode"

	"github.com/hollis-labs/nanite/internal/classify"
)

// ReflexMatch is the output of a successful matcher evaluation. It carries
// the winning Reflex plus the ScopeTier and ExecutionPattern hints that the
// dispatcher should forward to dispatch.AssignRole.
type ReflexMatch struct {
	// Reflex is the matching entry (full struct, not a pointer — callers
	// read fields; the original catalog slice is immutable).
	Reflex Reflex

	// HintTier is the ScopeTier hint to pass to AssignRole. Derived from
	// the reflex's ScopeTierHint when set; otherwise the m1Tier passed in.
	HintTier classify.ScopeTier

	// HintPattern is the ExecutionPattern hint to pass to AssignRole. Derived
	// from DispatchVia when mappable; otherwise the m1Pattern passed in.
	HintPattern classify.ExecutionPattern
}

// Match evaluates the registered reflex set against user input, returning the
// first winning reflex and true, or zero value and false on a miss.
//
// Contract (from docs/promptrouter-catalog.md § "Reflex matcher contract"):
//
//		match(user_input, m1_tier, m1_pattern) → (Reflex, bool)
//
//	  - Normalize user_input: lower-case, collapse whitespace.
//	  - Iterate reflexes in descending priority order (already sorted in
//	    BuiltinReflexes; caller-supplied sets are sorted here).
//	  - For each reflex, check:
//	    1. user_phrase_any_of — any phrase is a substring of normalized input.
//	    2. scope_tier_hint — if set, m1_tier must be >= hint tier.
//	    3. execution_pattern_hint — if set, m1_pattern must match exactly.
//	  - Return the first (highest-priority) match.
//
// reflexes must be sorted descending by Priority before calling (the return
// value of BuiltinReflexes and LoadUserReflexes already are).
func Match(input string, m1Tier classify.ScopeTier, m1Pattern classify.ExecutionPattern, reflexes []Reflex) (ReflexMatch, bool) {
	norm := normalizeInput(input)
	if norm == "" {
		return ReflexMatch{}, false
	}

	for _, r := range reflexes {
		if !phraseMatchAny(norm, r.Triggers.UserPhraseAnyOf) {
			continue
		}
		if r.Triggers.ScopeTierHint != 0 && !tierAtLeast(m1Tier, r.Triggers.ScopeTierHint) {
			continue
		}
		if r.Triggers.ExecutionPatternHint != 0 && m1Pattern != r.Triggers.ExecutionPatternHint {
			continue
		}

		return ReflexMatch{
			Reflex:      r,
			HintTier:    deriveTier(r, m1Tier),
			HintPattern: derivePattern(r, m1Pattern),
		}, true
	}

	return ReflexMatch{}, false
}

// MatchAll returns all matching reflexes sorted descending by priority.
// Used for diagnostic / logging purposes; production dispatch uses Match.
func MatchAll(input string, m1Tier classify.ScopeTier, m1Pattern classify.ExecutionPattern, reflexes []Reflex) []ReflexMatch {
	norm := normalizeInput(input)
	if norm == "" {
		return nil
	}

	var out []ReflexMatch
	for _, r := range reflexes {
		if !phraseMatchAny(norm, r.Triggers.UserPhraseAnyOf) {
			continue
		}
		if r.Triggers.ScopeTierHint != 0 && !tierAtLeast(m1Tier, r.Triggers.ScopeTierHint) {
			continue
		}
		if r.Triggers.ExecutionPatternHint != 0 && m1Pattern != r.Triggers.ExecutionPatternHint {
			continue
		}
		out = append(out, ReflexMatch{
			Reflex:      r,
			HintTier:    deriveTier(r, m1Tier),
			HintPattern: derivePattern(r, m1Pattern),
		})
	}
	return out
}

// normalizeInput lower-cases the input, collapses internal whitespace, and
// strips leading/trailing non-alphanumeric characters.
func normalizeInput(s string) string {
	s = strings.ToLower(s)
	// Collapse runs of whitespace to a single space.
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteRune(' ')
			}
			prevSpace = true
		} else {
			b.WriteRune(r)
			prevSpace = false
		}
	}
	return strings.TrimSpace(b.String())
}

// phraseMatchAny reports whether any phrase in the list is a substring of the
// normalized input. Phrases are already expected to be lower-case.
func phraseMatchAny(normInput string, phrases []string) bool {
	for _, p := range phrases {
		if strings.Contains(normInput, p) {
			return true
		}
	}
	return false
}

// tierAtLeast reports whether actual tier is >= required tier. Broader tiers
// have higher numeric values (TierOpen > TierLarge > TierMedium > ...).
// When actual is TierInvalid (0) we return false — no tier hint means no match
// for guard-carrying reflexes.
func tierAtLeast(actual, required classify.ScopeTier) bool {
	if actual == 0 {
		return false
	}
	return actual >= required
}

// deriveTier returns the ScopeTier hint to surface in ReflexMatch. The reflex's
// ScopeTierHint takes precedence when set; otherwise pass through the M1 tier.
func deriveTier(r Reflex, m1Tier classify.ScopeTier) classify.ScopeTier {
	if r.Triggers.ScopeTierHint != 0 {
		return r.Triggers.ScopeTierHint
	}
	return m1Tier
}

// derivePattern returns the ExecutionPattern hint to surface in ReflexMatch.
// Maps DispatchVia to an ExecutionPattern when recognizable; otherwise falls
// back to the M1 pattern.
func derivePattern(r Reflex, m1Pattern classify.ExecutionPattern) classify.ExecutionPattern {
	switch r.SideEffects.DispatchVia {
	case "executeBackground":
		return classify.PatternBackground
	case "executeTask":
		// "executeTask" means a sync subagent — map to PatternSubagent.
		return classify.PatternSubagent
	}
	// No DispatchVia — honour the ExecutionPatternHint guard if set, else M1.
	if r.Triggers.ExecutionPatternHint != 0 {
		return r.Triggers.ExecutionPatternHint
	}
	return m1Pattern
}
