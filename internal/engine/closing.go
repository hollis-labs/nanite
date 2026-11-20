package engine

import (
	"context"
	"errors"
	"strings"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/provider/keepalive"
)

// closingReply permits exactly one extra request, without tools or retries.
// Keep the original limit reason so callers still know work was cut short.
func (x *execution) closingReply(ctx context.Context, blocks []llmtypes.SlotBlock, text *strings.Builder) error {
	reason := x.result.StopReason
	defer func() { x.result.StopReason = reason }()
	if err := ctx.Err(); err != nil {
		return err
	}
	messages := requestMessages(x.messages, x.messageIDs)
	messages = append(messages, llmtypes.ChatMessage{Role: "user", Content: "The turn reached its limit. Give a concise final reply using only the information already available. State any unfinished work. Do not call tools."})
	req := llmtypes.ChatRequest{Model: x.definition.Runtime.Model, MaxTokens: x.definition.Runtime.MaxOutputTokens, SlotBlocks: blocks, Messages: messages,
		CacheHints: []llmtypes.CacheHint{{Position: "system"}}}
	spanCtx, end := events.ModelSpan(ctx, x.engine.scrub.Text(req.Model))
	defer end()
	callCtx, cancel := context.WithCancel(keepalive.WithIdle(spanCtx, x.engine.idleTimeout()))
	defer cancel()
	stream, err := x.provider.StreamChat(callCtx, req)
	if err != nil {
		return err
	}
	x.result.Steps++
	x.run.steps.Store(int64(x.result.Steps))
	got, err := x.consume(callCtx, stream)
	appendStepText(text, got.text)
	// Never execute a tool even if a provider ignores the tool-less request.
	// Keep any returned calls paired with cancellation results in the journal.
	if len(got.blocks) > 0 {
		saveErr := x.message("assistant", got.blocks)
		if saveErr == nil && len(got.calls) > 0 {
			saveErr = x.cancelledTools(got.calls)
			x.traceCancelled(got.calls)
		}
		err = errors.Join(err, saveErr)
	}
	return err
}
