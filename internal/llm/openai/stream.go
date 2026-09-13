package openai

import (
	"context"
	"errors"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/llm/toolargs"
	sdk "github.com/openai/openai-go/v3"
)

// StreamChat opens a streaming chat completion against the OpenAI API and
// translates the SDK's ChatCompletionChunk events into nanite's internal
// StreamEvent shape. The returned channel emits in order:
//
//  1. zero or more `delta` events (text fragments)
//  2. zero or more `tool_use` events (one per tool call, finalized when the
//     stream signals stop)
//  3. exactly one `usage` event (when the API includes usage in the final
//     chunk via `stream_options.include_usage`)
//  4. exactly one terminator: either `done` (stop reason translated into
//     Usage.StopReason) or `error` (transport / SDK failure).
//
// The channel is closed after the terminator event.
func (c *Client) StreamChat(ctx context.Context, req llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	if useResponses(ctx, req) {
		return c.streamResponse(ctx, req)
	}
	params, err := buildChatParams(req)
	if err != nil {
		return nil, err
	}
	// Ask OpenAI to include usage in the final chunk so we can emit a
	// usage StreamEvent before the terminator.
	params.StreamOptions.IncludeUsage = sdk.Bool(true)

	stream := c.sdk.Chat.Completions.NewStreaming(ctx, params)

	out := make(chan llmtypes.StreamEvent, 16)
	go func() {
		defer close(out)
		defer func() { _ = stream.Close() }()
		var (
			stopReason   string
			lastUsage    *llmtypes.Usage
			pendingTools = map[int64]*partialToolCall{}
			toolOrder    []int64
		)

		emit := func(ev llmtypes.StreamEvent) bool {
			select {
			case <-ctx.Done():
				return false
			case out <- ev:
				return true
			}
		}

		for stream.Next() {
			chunk := stream.Current()
			// Capture usage when present (final chunk under include_usage).
			if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 || chunk.Usage.TotalTokens > 0 {
				lastUsage = &llmtypes.Usage{
					InputTokens:  int(chunk.Usage.PromptTokens),
					OutputTokens: int(chunk.Usage.CompletionTokens),
				}
			}
			for _, choice := range chunk.Choices {
				if delta := choice.Delta.Content; delta != "" {
					if !emit(llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: delta}) {
						return
					}
				}
				for _, tc := range choice.Delta.ToolCalls {
					accumulateToolCall(pendingTools, &toolOrder, toolCallDelta{
						Index: tc.Index,
						ID:    tc.ID,
						Function: struct {
							Name      string
							Arguments string
						}{Name: tc.Function.Name, Arguments: tc.Function.Arguments},
					})
				}
				if choice.FinishReason != "" {
					stopReason = mapStopReason(choice.FinishReason)
				}
			}
		}
		if err := stream.Err(); err != nil {
			if errors.Is(err, context.Canceled) {
				_ = emit(llmtypes.StreamEvent{Type: llmtypes.EventError, Error: err.Error()})
				return
			}
			_ = emit(llmtypes.StreamEvent{Type: llmtypes.EventError, Error: translateError(err).Error()})
			return
		}

		// Emit any completed tool_use events in deterministic order (the
		// order in which the model first surfaced the call's index).
		for _, idx := range toolOrder {
			pt := pendingTools[idx]
			if pt == nil || pt.id == "" || pt.name == "" {
				continue
			}
			// AD-19: provider adapters degrade malformed streamed tool
			// arguments to {"_raw": raw} instead of aborting the turn.
			input := toolargs.ParseObject(pt.arguments.String())
			if !emit(llmtypes.StreamEvent{
				Type: llmtypes.EventToolUse,
				ToolUse: &llmtypes.ToolUseBlock{
					ID:    pt.id,
					Name:  pt.name,
					Input: input,
				},
			}) {
				return
			}
		}

		if lastUsage != nil {
			lastUsage.StopReason = stopReason
			if !emit(llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: lastUsage}) {
				return
			}
		}

		_ = emit(llmtypes.StreamEvent{Type: llmtypes.EventDone})
	}()

	return out, nil
}

// partialToolCall accumulates an in-flight tool call. The OpenAI streaming
// API delivers tool_calls as deltas keyed by an index field — the ID and
// name typically arrive on the first delta with arguments streamed across
// subsequent deltas as one or more JSON fragments.
type partialToolCall struct {
	id        string
	name      string
	arguments stringBuf
}

func accumulateToolCall(buf map[int64]*partialToolCall, order *[]int64, tc toolCallDelta) {
	pt, exists := buf[tc.Index]
	if !exists {
		pt = &partialToolCall{}
		buf[tc.Index] = pt
		*order = append(*order, tc.Index)
	}
	if tc.ID != "" {
		pt.id = tc.ID
	}
	if tc.Function.Name != "" {
		pt.name = tc.Function.Name
	}
	if tc.Function.Arguments != "" {
		pt.arguments.WriteString(tc.Function.Arguments)
	}
}

// toolCallDelta is the subset of the SDK's
// ChatCompletionChunkChoiceDeltaToolCall we read; declaring an internal
// struct keeps accumulateToolCall testable without spinning up the SDK
// types in tests.
type toolCallDelta struct {
	Index    int64
	ID       string
	Function struct {
		Name      string
		Arguments string
	}
}

// stringBuf is a tiny strings.Builder substitute that supports zero-value
// use without an explicit reset.
type stringBuf struct{ b []byte }

func (s *stringBuf) WriteString(v string) { s.b = append(s.b, v...) }
func (s *stringBuf) String() string       { return string(s.b) }

// mapStopReason normalizes OpenAI finish reasons into the same vocabulary
// nanite uses across providers (see chat_generate.go's reading of
// Usage.StopReason). "tool_calls" → "tool_use" matches the existing
// Anthropic provider's convention.
func mapStopReason(r string) string {
	switch r {
	case "tool_calls":
		return "tool_use"
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "content_filter":
		return "content_filter"
	default:
		return r
	}
}
