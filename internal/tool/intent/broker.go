package intent

import (
	"context"
	"fmt"
	"strings"
)

// Plan §D3 broker thresholds. Above High, rules wins outright. Below Low, no
// hydration. In between, fall through to the LLM.
const (
	BrokerRulesHigh = 0.7
	BrokerRulesLow  = 0.3
)

// BrokerClassifier composes the three layers in plan §D3 order:
//
//  1. Explicit signals (session /tools state + literal tool-name mentions)
//  2. Rules (local keyword scoring)
//  3. LLM (ambiguous cases only)
//
// On a short-circuit at any layer the deeper layers are not invoked. D9
// fail-open applies to the LLM layer — see LLMClassifier.
type BrokerClassifier struct {
	rules *RulesClassifier
	llm   Classifier // optional; nil disables layer 3
}

// NewBrokerClassifier wires the broker from its two inner layers. llm may be
// nil for rules-only operation (e.g., when UserSettings.ToolClassifierMode is
// "rules" or when no provider is available).
func NewBrokerClassifier(rules *RulesClassifier, llm Classifier) *BrokerClassifier {
	if rules == nil {
		rules = NewRulesClassifier()
	}
	return &BrokerClassifier{rules: rules, llm: llm}
}

// Classify runs the D3 layers in order.
func (b *BrokerClassifier) Classify(ctx context.Context, in Input) (Result, error) {
	// Layer 3 (explicit signals) — short-circuits both rules and LLM.
	if r, ok := explicitDecision(in); ok {
		return r, nil
	}

	// Layer 1 (rules).
	rulesResult, err := b.rules.Classify(ctx, in)
	if err != nil {
		return rulesResult, err
	}
	if rulesResult.Confidence >= BrokerRulesHigh {
		return rulesResult, nil
	}
	if rulesResult.Confidence <= BrokerRulesLow && !rulesResult.Hydrate {
		return rulesResult, nil
	}

	// Layer 2 (LLM) — ambiguous zone.
	if b.llm == nil {
		return rulesResult, nil
	}
	llmResult, err := b.llm.Classify(ctx, in)
	if err != nil {
		// LLMClassifier's own fail-open applies — err is informational.
		return llmResult, nil
	}
	return llmResult, nil
}

// explicitDecision detects the Layer-3 user signals (plan §D3) that override
// everything else:
//
//   - OverrideOn / OverrideOff (`/tools on|off` session state)
//   - A literal tool name mentioned in the user turn
//
// Returns (zero, false) when no explicit signal applies.
func explicitDecision(in Input) (Result, bool) {
	switch in.Override {
	case OverrideOn:
		return Result{
			Hydrate:    true,
			Categories: nil, // hydrate all
			Confidence: 1.0,
			Source:     SourceExplicit,
			Reasoning:  "User pinned tools ON via `/tools on`.",
		}, true
	case OverrideOff:
		return Result{
			Hydrate:    false,
			Confidence: 1.0,
			Source:     SourceExplicit,
			Reasoning:  "User pinned tools OFF via `/tools off`.",
		}, true
	}

	if name := firstMentionedToolName(in.UserTurn, in.ToolNames); name != "" {
		return Result{
			Hydrate:    true,
			Categories: nil, // hydrate all — we don't know which category the tool is from at this layer
			Confidence: 1.0,
			Source:     SourceExplicit,
			Reasoning:  fmt.Sprintf("User mentioned tool %q by name.", name),
		}, true
	}

	return Result{}, false
}

// firstMentionedToolName returns the first tool whose name appears as a
// whole-word substring of text, or "" if none. Case-insensitive.
func firstMentionedToolName(text string, names []string) string {
	if text == "" || len(names) == 0 {
		return ""
	}
	lower := strings.ToLower(text)
	for _, n := range names {
		if n == "" {
			continue
		}
		ln := strings.ToLower(n)
		// Require word-boundary-ish match so "search" tool doesn't fire on "searching".
		if idx := strings.Index(lower, ln); idx >= 0 {
			if isWordBoundary(lower, idx, len(ln)) {
				return n
			}
		}
	}
	return ""
}

func isWordBoundary(text string, start, length int) bool {
	end := start + length
	leftOK := start == 0 || !isWordChar(text[start-1])
	rightOK := end == len(text) || !isWordChar(text[end])
	return leftOK && rightOK
}

func isWordChar(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z':
		return true
	case b >= 'A' && b <= 'Z':
		return true
	case b >= '0' && b <= '9':
		return true
	case b == '_':
		return true
	}
	return false
}
