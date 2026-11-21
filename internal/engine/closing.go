package engine

import (
	"context"
	"errors"
	"strings"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/provider/keepalive"
	"github.com/hollis-labs/nanite/internal/provider/toolchoice"
	"github.com/hollis-labs/nanite/internal/recovery"
)

// closingReply permits exactly one extra request, with tool calls disabled and no retries.
// Keep the original limit reason so callers still know work was cut short.
func (x *execution) closingReply(ctx context.Context, req llmtypes.ChatRequest, text *strings.Builder) error {
	if err := x.engine.checkDaily(); err != nil {
		return err
	}
	reason := x.result.StopReason
	defer func() { x.result.StopReason = reason }()
	if err := ctx.Err(); err != nil {
		return err
	}
	// Persist the recovery marker even though this is a host note, not a new
	// user turn. History and compaction classify it by that marker.
	if err := x.message("user", []llmtypes.ContentBlock{{Type: "text", Name: recovery.ClosingPromptPrefix + reason,
		Text: "The turn reached its limit. Give a concise final reply using only the information already available. State any unfinished work. Do not call tools."}}); err != nil {
		return err
	}
	req.Messages = requestMessages(x.messages, x.messageIDs)
	spanCtx, end := events.ModelSpan(ctx, x.engine.scrub.Text(req.Model))
	defer end()
	callCtx, cancel := context.WithCancel(toolchoice.WithNone(keepalive.WithIdle(spanCtx, x.engine.idleTimeout())))
	defer cancel()
	stream, err := x.provider.StreamChat(callCtx, req)
	if err != nil {
		return err
	}
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
