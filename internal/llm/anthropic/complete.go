package anthropic

import (
	"context"
	"fmt"
	"strings"

	llmcontracts "github.com/hollis-labs/libs/llm/core/llmcontracts"
	llmtypes "github.com/hollis-labs/libs/llm/core/llmtypes"
)

// Complete implements llmcontracts.Provider.Complete. Non-streaming
// completion. Returns the concatenated text from all "text" content blocks.
//
// Mirrors the deleted adapter's behavior: returns an error when the
// response had no text blocks (rather than an empty string) so callers
// can distinguish "no content" from "tool_use only".
func (c *Client) Complete(ctx context.Context, in llmtypes.ChatRequest) (string, error) {
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
	params := c.buildMessageParams(nonStreamingRequest(in), model, interleavedThinking, reasoningCfg)

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
	if b.Len() == 0 {
		return "", fmt.Errorf("anthropic complete: response had no text blocks")
	}
	return b.String(), nil
}
