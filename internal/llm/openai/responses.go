package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/llm/toolargs"
	"github.com/openai/openai-go/v3/responses"
)

func (c *Client) completeResponse(ctx context.Context, req llmtypes.ChatRequest) (string, error) {
	params, err := buildResponseParams(ctx, req)
	if err != nil {
		return "", err
	}
	response, err := c.sdk.Responses.New(ctx, params)
	if err != nil {
		return "", translateError(err)
	}
	if err := responseFailure(*response); err != nil {
		return "", err
	}
	if text := response.OutputText(); text != "" {
		return text, nil
	}
	return "", errEmptyResponse
}

func responseFailure(response responses.Response) error {
	if response.Error.Message != "" {
		return fmt.Errorf("openai: %s: %s", response.Error.Code, response.Error.Message)
	}
	if response.Status == "incomplete" {
		return fmt.Errorf("openai: response incomplete: %s", response.IncompleteDetails.Reason)
	}
	if response.Status == responses.ResponseStatusFailed || response.Status == responses.ResponseStatusCancelled {
		return fmt.Errorf("openai: response %s", response.Status)
	}
	return nil
}

func responseUsage(response responses.Response, stopReason string) *llmtypes.Usage {
	return &llmtypes.Usage{
		InputTokens: int(response.Usage.InputTokens), OutputTokens: int(response.Usage.OutputTokens),
		CacheReadTokens: int(response.Usage.InputTokensDetails.CachedTokens), StopReason: stopReason,
	}
}

func (c *Client) streamResponse(ctx context.Context, req llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	params, err := buildResponseParams(ctx, req)
	if err != nil {
		return nil, err
	}
	stream := c.sdk.Responses.NewStreaming(ctx, params)
	out := make(chan llmtypes.StreamEvent, 16)
	go func() {
		defer close(out)
		defer func() { _ = stream.Close() }()
		emit := func(event llmtypes.StreamEvent) bool {
			select {
			case <-ctx.Done():
				return false
			case out <- event:
				return true
			}
		}
		fail := func(err error) {
			emit(llmtypes.StreamEvent{Type: llmtypes.EventError, Error: err.Error()})
		}
		for stream.Next() {
			event := stream.Current()
			switch event.Type {
			case "response.output_text.delta", "response.refusal.delta":
				if !emit(llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: event.Delta}) {
					return
				}
			case "response.reasoning_summary_text.delta":
				if !emit(llmtypes.StreamEvent{Type: llmtypes.EventThinking, ThinkingBlock: &llmtypes.ThinkingBlock{Thinking: event.Delta}}) {
					return
				}
			case "error":
				fail(fmt.Errorf("openai: %s: %s", event.Code, event.Message))
				return
			case "response.failed", "response.incomplete":
				stopReason := "error"
				if event.Response.IncompleteDetails.Reason == "max_output_tokens" {
					stopReason = "max_tokens"
				}
				emit(llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: responseUsage(event.Response, stopReason)})
				err := responseFailure(event.Response)
				if err == nil {
					err = fmt.Errorf("openai: %s", event.Type)
				}
				fail(err)
				return
			case "response.completed":
				if err := responseFailure(event.Response); err != nil {
					fail(err)
					return
				}
				// Only execute tools from a successfully completed response, never
				// from partial argument deltas or a failed/truncated response.
				rawOutput := make([]json.RawMessage, 0, len(event.Response.Output))
				stopReason := "end_turn"
				for _, item := range event.Response.Output {
					rawOutput = append(rawOutput, json.RawMessage(item.RawJSON()))
					if item.Type == "function_call" {
						stopReason = "tool_use"
					}
				}
				encoded, err := json.Marshal(rawOutput)
				if err != nil {
					fail(fmt.Errorf("openai: preserve response output: %w", err))
					return
				}
				if !emit(llmtypes.StreamEvent{Type: responseOutputBlock, Content: string(encoded)}) {
					return
				}
				for _, item := range event.Response.Output {
					if item.Type != "function_call" {
						continue
					}
					if item.CallID == "" || item.Name == "" {
						fail(fmt.Errorf("openai: function call is missing its call ID or name"))
						return
					}
					if !emit(llmtypes.StreamEvent{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{
						ID: item.CallID, Name: item.Name, Input: toolargs.ParseObject(item.AsFunctionCall().Arguments),
					}}) {
						return
					}
				}
				if emit(llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: responseUsage(event.Response, stopReason)}) {
					emit(llmtypes.StreamEvent{Type: llmtypes.EventDone})
				}
				return
			}
		}
		if err := stream.Err(); err != nil {
			fail(translateError(err))
		} else {
			// A clean socket EOF does not mean the provider finished the turn.
			fail(fmt.Errorf("openai: response stream ended before completion: %w", io.ErrUnexpectedEOF))
		}
	}()
	return out, nil
}
