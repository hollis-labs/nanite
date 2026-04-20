// Package classify provides the P3 ScopeTier + ExecutionPattern primitive.
// See internal/classify/doc.go for the full consumer contract.
package classify

// ScopeTier is the "how big" classifier. It controls downstream budget shape.
// Orthogonal to ExecutionPattern (D1).
type ScopeTier int

const (
	TierInvalid ScopeTier = iota
	TierTrivial
	TierSmall
	TierMedium
	TierLarge
	TierOpen
)

// String returns the canonical lower-case name for a ScopeTier.
func (t ScopeTier) String() string {
	switch t {
	case TierTrivial:
		return "trivial"
	case TierSmall:
		return "small"
	case TierMedium:
		return "medium"
	case TierLarge:
		return "large"
	case TierOpen:
		return "open"
	default:
		return "invalid"
	}
}

// IsValid reports whether t is a known tier (not TierInvalid or out-of-range).
func (t ScopeTier) IsValid() bool {
	return t >= TierTrivial && t <= TierOpen
}

// ExecutionPattern is the "how to run" classifier. It controls downstream
// dispatch shape. Orthogonal to ScopeTier (D1).
type ExecutionPattern int

const (
	PatternInvalid ExecutionPattern = iota
	PatternInline
	PatternSubagent
	PatternBackground
)

// String returns the canonical lower-case name for an ExecutionPattern.
func (p ExecutionPattern) String() string {
	switch p {
	case PatternInline:
		return "inline"
	case PatternSubagent:
		return "subagent"
	case PatternBackground:
		return "background"
	default:
		return "invalid"
	}
}

// IsValid reports whether p is a known pattern (not PatternInvalid or out-of-range).
func (p ExecutionPattern) IsValid() bool {
	return p >= PatternInline && p <= PatternBackground
}

// IntentSignals carries the pre-loop information Classify uses. Callers
// populate this struct once, at the generateResponse boundary, before
// newLoopState runs.
type IntentSignals struct {
	// Message is the user's current prompt text, verbatim.
	Message string

	// MessageTokenEst is a rough token count for Message. Callers may
	// compute this as len(Message)/4 (the coarse byte-to-token heuristic
	// internal/context already uses); exact tokenizer-based counts are
	// acceptable but not required.
	MessageTokenEst int

	// HasAttachments reports whether the user attached structured content
	// (files, cards, session objects) to the message.
	HasAttachments bool

	// ToolsAvailable is the count of tools the harness has loaded for this
	// generation. Used only as a weak scope signal — a richer toolset
	// implies a larger-scoped workspace.
	ToolsAvailable int
}
