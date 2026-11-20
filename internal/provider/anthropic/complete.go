package anthropic

import (
	"context"
	"fmt"
	"strings"

	llmcontracts "github.com/hollis-labs/libs/llm/core/llmcontracts"
	llmtypes "github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/provider/failure"
)

// Complete implements llmcontracts.Provider.Complete. Non-streaming
// completion. Returns the concatenated text from all "text" content blocks.
//
// A response with no text, or only whitespace, returns an error wrapping
// failure.ErrEmptyResponse rather than an empty string.
func (c *Client) Complete(ctx context.Context, in llmtypes.ChatRequest) (string, error) {
	if err := c.validateCacheTTLs(in); err != nil {
		return "", err
	}
	api, err := c.client(ctx)
	if err != nil {
		return "", err
	}

	model, err := resolveModel(in)
	if err != nil {
		return "", err
	}
	reasoningCfg := llmcontracts.ReasoningConfigFromContext(ctx)
	interleavedThinking := c.interleavedThinking(reasoningCfg, model)
	params := c.buildRequestParams(ctx, nonStreamingRequest(in), model, interleavedThinking, reasoningCfg)

	opts := c.requestOptions(ctx, interleavedThinking)
	resp, err := api.Messages.New(ctx, params, opts...)
	if err != nil {
		return "", translateError(err)
	}

	var b strings.Builder
	for _, block := range resp.Content {
		if block.Type == "text" {
			b.WriteString(block.Text)
		}
	}
	if strings.TrimSpace(b.String()) == "" {
		return "", fmt.Errorf("anthropic complete: %w", failure.ErrEmptyResponse)
	}
	return b.String(), nil
}
