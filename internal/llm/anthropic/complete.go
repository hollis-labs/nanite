package anthropic

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/option"
	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
)

// Complete implements llmcontracts.Provider.Complete. Non-streaming
// completion. Returns the concatenated text from all "text" content blocks.
//
// Mirrors the deleted adapter's behaviour: returns an error when the
// response had no text blocks (rather than an empty string) so callers
// can distinguish "no content" from "tool_use only".
func (c *Client) Complete(ctx context.Context, in llmtypes.ChatRequest) (string, error) {
	if c.apiKey == "" {
		return "", errors.New("ANTHROPIC_API_KEY not set")
	}

	model := resolveModel(in)
	reasoningCfg := llmcontracts.ReasoningConfigFromContext(ctx)
	interleavedThinking := shouldEnableInterleavedThinking(reasoningCfg, model)
	params := c.buildMessageParams(in, model, interleavedThinking, reasoningCfg)

	opts := []option.RequestOption{betaHeaderRequestOption(interleavedThinking)}
	resp, err := c.sdk.Messages.New(ctx, params, opts...)
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
