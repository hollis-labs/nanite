package context

import (
	"context"
	"fmt"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
)

// ProviderSummarizer implements Summarizer against a llmcontracts.Provider. It
// issues a single non-streaming Complete call with the mode-aware system
// prompt and returns the response text.
type ProviderSummarizer struct {
	Provider llmcontracts.Provider
	Model    string
}

// NewProviderSummarizer constructs a ProviderSummarizer. Model may be empty
// to fall through to the provider's default.
func NewProviderSummarizer(p llmcontracts.Provider, model string) *ProviderSummarizer {
	return &ProviderSummarizer{Provider: p, Model: model}
}

// Summarize implements Summarizer.
func (s *ProviderSummarizer) Summarize(ctx context.Context, systemPrompt string, messages []llmtypes.ChatMessage) (string, error) {
	if s == nil || s.Provider == nil {
		return "", fmt.Errorf("summarizer: no provider configured")
	}
	out, err := s.Provider.Complete(ctx, llmtypes.ChatRequest{
		SystemPrompt: systemPrompt,
		Messages:     messages,
		Model:        s.Model,
	})
	if err != nil {
		return "", fmt.Errorf("summarizer complete: %w", err)
	}
	return out, nil
}
