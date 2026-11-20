package openai

import (
	"context"
	"errors"

	"github.com/hollis-labs/nanite/internal/provider/failure"
	"github.com/hollis-labs/nanite/internal/provider/keepalive"

	llmtypes "github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/provider/toolargs"
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
	if c.responses(ctx, req) {
		return c.streamResponse(ctx, req)
	}
	params, err := buildChatParams(req)
	if err != nil {
		return nil, err
	}
	// Ask OpenAI to include usage in the final chunk so we can emit a
	// usage StreamEvent before the terminator.
	params.StreamOptions.IncludeUsage = sdk.Bool(true)

	api, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	stream := api.Chat.Completions.NewStreaming(ctx, params, c.requestOptions(ctx)...)

	out := make(chan llmtypes.StreamEvent, 16)
	failure.Guard(c.panics).Go("openai stream", func() {
		defer close(out)
		defer failure.RecoverStream(ctx, c.panics, "openai stream", out)
		defer func() { _ = stream.Close() }()
		var (
			stopReason   string
			lastUsage    *llmtypes.Usage
			pendingTools = map[int64]*partialToolCall{}
			toolOrder    []int64
		)

		busy := keepalive.Start(ctx)
		emit := func(ev llmtypes.StreamEvent) bool {
			select {
			case <-ctx.Done():
				return false
			case out <- ev:
				busy.Sent()
				return true
			}
		}

		for stream.Next() {
			chunk := stream.Current()
			// Tool-call arguments are kept until the stream ends; say the
			// stream is busy while they arrive.
			if busy.Due() && !emit(llmtypes.StreamEvent{Type: keepalive.Event}) {
				return
			}
			// Capture usage when present (final chunk under include_usage).
			if chunk.JSON.Usage.Valid() {
				lastUsage = &llmtypes.Usage{
					InputTokens:         uncachedInput(chunk.Usage.PromptTokens, chunk.Usage.PromptTokensDetails.CachedTokens, chunk.Usage.PromptTokensDetails.CacheWriteTokens),
					OutputTokens:        int(chunk.Usage.CompletionTokens),
					CacheReadTokens:     int(chunk.Usage.PromptTokensDetails.CachedTokens),
					CacheCreationTokens: int(chunk.Usage.PromptTokensDetails.CacheWriteTokens),
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
				_ = emit(failure.Event(err))
				return
			}
			_ = emit(failure.Event(translateError(err)))
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
	}, nil)

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

// mapStopReason maps OpenAI finish reasons to the shared stop reasons.
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

// uncachedInput converts OpenAI's prompt count, which includes cached and
// cache-written tokens, to the meaning Usage.InputTokens has across Nanite:
// input that was neither read from nor written to the cache, as the
// Anthropic wire reports it. Without this, every consumer that adds
// InputTokens and the cache counts counted cached tokens twice.
func uncachedInput(prompt, cached, written int64) int {
	return int(max(0, prompt-cached-written))
}
