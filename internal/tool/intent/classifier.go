// Package intent decides whether the current user turn needs tools, and if so
// which categories to hydrate into the Tools slot. It is composed of three
// layers (plan §D3):
//
//   - RulesClassifier: keyword → category weights; fast + local.
//   - LLMClassifier: a small LLM call for ambiguous cases.
//   - BrokerClassifier: composes rules + LLM + explicit user signals
//     (`/tools on|off` + literal tool-name mentions).
//
// The result drives AssembleSlots: hydrate=true fills the Tools slot with the
// full definitions for the returned categories; hydrate=false leaves the slot
// holding the stash's pointer-summary.
package intent

import "context"

// Source identifies which layer produced a result. Used in telemetry + the
// slot_changed envelope's reasoning field.
const (
	SourceRules    = "rules"
	SourceLLM      = "llm"
	SourceExplicit = "explicit"
	SourceFallback = "fallback"
)

// Override carries the user's explicit `/tools on|off` state for the session.
// OverrideOn forces hydrate; OverrideOff forces pointer.
type Override int

const (
	OverrideNone Override = iota
	OverrideOn
	OverrideOff
)

// Input is the per-turn input to a classifier.
type Input struct {
	// UserTurn is the latest user message text (may be empty on agent-only turns).
	UserTurn string
	// AvailableCategories is the set of categories the current stash provides.
	// Classifier results are restricted to this set.
	AvailableCategories []string
	// ToolNames is the set of tool names currently in scope; used by the
	// explicit-signals layer to detect literal tool-name mentions.
	ToolNames []string
	// Override is the session's /tools on|off state; OverrideNone means the
	// user hasn't explicitly pinned the slot.
	Override Override
}

// Result is a classifier's decision.
type Result struct {
	// Hydrate is true when the Tools slot should carry full definitions for
	// this turn; false keeps the pointer-summary.
	Hydrate bool
	// Categories is the subset of Input.AvailableCategories to hydrate when
	// Hydrate=true. Empty + Hydrate=true means "hydrate all".
	Categories []string
	// Confidence is a rough score in [0, 1]; used by BrokerClassifier to
	// decide whether to fall through to the LLM layer.
	Confidence float64
	// Source is one of the Source* constants — which layer produced this.
	Source string
	// Reasoning is a short human-readable explanation that surfaces in the
	// slot_changed envelope.
	Reasoning string
}

// Classifier decides whether a user turn requires tools.
type Classifier interface {
	Classify(ctx context.Context, in Input) (Result, error)
}
