package openai

import (
	"context"
	"strings"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/openai/openai-go/v3/shared"
)

// These compatibility rules cover known OpenAI families, not future major
// versions. Replace them with model capabilities when CW-20260912-0108 lands.
func isAstra(model string) bool {
	return model == "gpt-6-astra" || strings.HasPrefix(model, "gpt-6-astra-")
}

func modelSupportsReasoning(model string) bool {
	if strings.Contains(model, "-chat") {
		return false
	}
	for _, family := range []string{"gpt-5", "o1", "o3", "o4"} {
		if model == family || strings.HasPrefix(model, family+"-") || strings.HasPrefix(model, family+".") {
			return true
		}
	}
	return isAstra(model)
}

func modelSupportsReasoningNone(model string) bool {
	if !modelSupportsReasoning(model) {
		return false
	}
	for _, family := range []string{"gpt-5.1", "gpt-5.2", "gpt-5.3", "gpt-5.4", "gpt-5.5", "gpt-5.6"} {
		if model == family || strings.HasPrefix(model, family+"-") {
			return true
		}
	}
	return false
}

func useResponses(ctx context.Context, req llmtypes.ChatRequest) bool {
	if isAstra(req.Model) {
		return true
	}
	cfg := llmcontracts.ReasoningConfigFromContext(ctx)
	return modelSupportsReasoning(req.Model) &&
		((cfg.Enabled && cfg.BudgetTokens > 0) || (len(req.Tools) > 0 && !modelSupportsReasoningNone(req.Model)))
}

// Nanite's 8k/20k hints are Anthropic token budgets, not OpenAI token limits.
// Map those two enabled tiers to medium/high; use low when the model cannot
// disable reasoning. This retains a useful distinction across all three tiers.
func responseReasoning(ctx context.Context) shared.ReasoningParam {
	effort := shared.ReasoningEffortLow
	cfg := llmcontracts.ReasoningConfigFromContext(ctx)
	if cfg.Enabled && cfg.BudgetTokens > 0 {
		effort = shared.ReasoningEffortMedium
		if cfg.BudgetTokens >= 20000 {
			effort = shared.ReasoningEffortHigh
		}
	}
	return shared.ReasoningParam{Effort: effort, Summary: shared.ReasoningSummaryAuto}
}
