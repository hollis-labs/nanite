package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// TurnStreamStarter begins the provider stream for one model invocation.
// Keeping stream construction behind a closure lets ExecuteTurn remain
// independent of provider and request shape.
type TurnStreamStarter func(context.Context) (<-chan llmtypes.StreamEvent, error)

// TurnSink receives incremental output synchronously, in provider event order.
// Every callback is optional.
type TurnSink struct {
	OnDelta    func(content string)
	OnToolUse  func(toolUse llmtypes.ToolUseBlock)
	OnThinking func(thinking llmtypes.ThinkingBlock)
}

// TurnRequest describes exactly one model invocation. IdleTimeout bounds the
// gap between provider events, not the total duration of the invocation.
type TurnRequest struct {
	Stream      TurnStreamStarter
	IdleTimeout time.Duration
	Sink        TurnSink
}

// TurnResult contains everything produced by one model invocation.
type TurnResult struct {
	Text           string
	ToolUseBlocks  []llmtypes.ToolUseBlock
	ThinkingBlocks []llmtypes.ThinkingBlock
	Usage          *llmtypes.Usage
	StopReason     string
	Stalled        bool
}

// TurnErrorKind identifies which boundary of a Turn failed. Callers can use
// errors.As to branch without parsing error text. TurnErrorStart preserves a
// starter's original error through Unwrap; TurnErrorStream represents an error
// after the stream started; TurnErrorStalled represents watchdog expiry.
type TurnErrorKind string

const (
	TurnErrorStart   TurnErrorKind = "start"
	TurnErrorStream  TurnErrorKind = "stream"
	TurnErrorStalled TurnErrorKind = "stalled"
)

// ErrTurnStalled is wrapped by a stalled TurnError, allowing errors.Is checks
// when a caller only needs the stall classification.
var ErrTurnStalled = errors.New("turn stream stalled")

// TurnError is the typed failure returned by ExecuteTurn.
type TurnError struct {
	Kind TurnErrorKind
	Err  error
}

func (e *TurnError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err == nil {
		return fmt.Sprintf("turn %s failed", e.Kind)
	}
	return fmt.Sprintf("turn %s failed: %v", e.Kind, e.Err)
}

func (e *TurnError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ExecuteTurn starts and consumes exactly one provider stream. It deliberately
// does not execute tools, classify failures, choose retries, or create tracing
// spans; those are Run-level responsibilities owned by its callers.
func ExecuteTurn(ctx context.Context, req TurnRequest) (TurnResult, error) {
	var result TurnResult
	if req.Stream == nil {
		return result, &TurnError{Kind: TurnErrorStart, Err: errors.New("nil stream starter")}
	}
	if req.IdleTimeout <= 0 {
		return result, &TurnError{Kind: TurnErrorStart, Err: errors.New("idle timeout must be positive")}
	}
	if err := ctx.Err(); err != nil {
		return result, &TurnError{Kind: TurnErrorStream, Err: context.Cause(ctx)}
	}

	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()

	events, err := req.Stream(streamCtx)
	if err != nil {
		return result, &TurnError{Kind: TurnErrorStart, Err: err}
	}

	streamIdle := time.NewTimer(req.IdleTimeout)
	defer streamIdle.Stop()

	var text strings.Builder
	for {
		// Give caller cancellation precedence over a simultaneously ready
		// provider event or channel closure. Without this check, select could
		// nondeterministically report an empty successful Turn after cancel.
		if err := streamCtx.Err(); err != nil {
			result.Text = text.String()
			return result, &TurnError{Kind: TurnErrorStream, Err: context.Cause(streamCtx)}
		}

		var event llmtypes.StreamEvent
		var ok bool
		select {
		case event, ok = <-events:
			if err := streamCtx.Err(); err != nil {
				result.Text = text.String()
				return result, &TurnError{Kind: TurnErrorStream, Err: context.Cause(streamCtx)}
			}
			if !ok {
				result.Text = text.String()
				return result, nil
			}
			// Stop-then-drain-then-reset is the race-free Timer reset idiom
			// used by the existing chat provider stream loop.
			if !streamIdle.Stop() {
				select {
				case <-streamIdle.C:
				default:
				}
			}
			streamIdle.Reset(req.IdleTimeout)
		case <-streamIdle.C:
			cancelStream()
			result.Text = text.String()
			result.Stalled = true
			return result, &TurnError{
				Kind: TurnErrorStalled,
				Err:  fmt.Errorf("%w: no provider events for %s", ErrTurnStalled, req.IdleTimeout),
			}
		case <-streamCtx.Done():
			result.Text = text.String()
			return result, &TurnError{Kind: TurnErrorStream, Err: context.Cause(streamCtx)}
		}

		switch event.Type {
		case llmtypes.EventDelta:
			text.WriteString(event.Content)
			if req.Sink.OnDelta != nil {
				req.Sink.OnDelta(event.Content)
			}

		case llmtypes.EventToolUse:
			if event.ToolUse != nil {
				result.ToolUseBlocks = append(result.ToolUseBlocks, *event.ToolUse)
				if req.Sink.OnToolUse != nil {
					req.Sink.OnToolUse(*event.ToolUse)
				}
			}

		case llmtypes.EventThinking:
			if event.ThinkingBlock != nil {
				result.ThinkingBlocks = append(result.ThinkingBlocks, *event.ThinkingBlock)
				if req.Sink.OnThinking != nil {
					req.Sink.OnThinking(*event.ThinkingBlock)
				}
			}

		case llmtypes.EventUsage:
			if event.Usage != nil {
				if result.Usage == nil {
					result.Usage = &llmtypes.Usage{}
				}
				if event.Usage.InputTokens > 0 {
					result.Usage.InputTokens += event.Usage.InputTokens
				}
				if event.Usage.OutputTokens > 0 {
					result.Usage.OutputTokens += event.Usage.OutputTokens
				}
				if event.Usage.CacheCreationTokens > 0 {
					result.Usage.CacheCreationTokens += event.Usage.CacheCreationTokens
				}
				if event.Usage.CacheReadTokens > 0 {
					result.Usage.CacheReadTokens += event.Usage.CacheReadTokens
				}
				if event.Usage.StopReason != "" {
					result.StopReason = event.Usage.StopReason
					result.Usage.StopReason = event.Usage.StopReason
				}
			}

		case llmtypes.EventError:
			result.Text = text.String()
			streamErr := errors.New(event.Error)
			if event.Error == "" {
				streamErr = errors.New("provider emitted an empty stream error")
			}
			return result, &TurnError{Kind: TurnErrorStream, Err: streamErr}

		case llmtypes.EventSessionID, llmtypes.EventDone:
			// These events are intentionally inert, matching the current
			// provider stream loop. Channel closure ends the Turn.
		}
	}
}
