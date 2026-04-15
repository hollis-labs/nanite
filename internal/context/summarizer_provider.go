package context

import (
	"context"
	"fmt"

	"github.com/hollis-labs/go-providers/provider"
)

// ProviderSummarizer implements Summarizer against a provider.Provider. It
// issues a single non-streaming Complete call with the mode-aware system
// prompt and returns the response text.
type ProviderSummarizer struct {
	Provider provider.Provider
	Model    string
}

// NewProviderSummarizer constructs a ProviderSummarizer. Model may be empty
// to fall through to the provider's default.
func NewProviderSummarizer(p provider.Provider, model string) *ProviderSummarizer {
	return &ProviderSummarizer{Provider: p, Model: model}
}

// Summarize implements Summarizer.
func (s *ProviderSummarizer) Summarize(ctx context.Context, systemPrompt string, messages []provider.ChatMessage) (string, error) {
	if s == nil || s.Provider == nil {
		return "", fmt.Errorf("summarizer: no provider configured")
	}
	out, err := s.Provider.Complete(ctx, provider.ChatRequest{
		SystemPrompt: systemPrompt,
		Messages:     messages,
		Model:        s.Model,
	})
	if err != nil {
		return "", fmt.Errorf("summarizer complete: %w", err)
	}
	return out, nil
}
