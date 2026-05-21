package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
)

// StreamChat implements llmcontracts.Provider.StreamChat. Mirrors the
// behavior of the deleted hand-rolled adapter:
//
//  1. Pre-flight: estimate request tokens (cache-aware), reject with
//     ErrRequestExceedsRateBudget when the request alone is bigger than the
//     observed per-minute window. Sleep when within budget after pacing.
//  2. Open the SDK stream via Messages.NewStreaming.
//  3. Translate SDK MessageStreamEventUnion events into internal
//     llmtypes.StreamEvents on a buffered channel; close on completion or
//     error.
//
// The middleware (option.WithMiddleware) handles header calibration and
// breaker state as the request flows through; this method just opens the
// stream and translates events.
func (c *Client) StreamChat(ctx context.Context, in llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	if c.apiKey == "" {
		return nil, errors.New("ANTHROPIC_API_KEY not set")
	}

	model := resolveModel(in)
	reasoningCfg := llmcontracts.ReasoningConfigFromContext(ctx)
	interleavedThinking := shouldEnableInterleavedThinking(reasoningCfg, model)
	params := c.buildMessageParams(in, model, interleavedThinking, reasoningCfg)

	// Marshal the params once so the rate-budget pre-flight estimate uses
	// the exact bytes the SDK will send. Same heuristic as the deleted
	// adapter: payload bytes / 4 minus the cacheable prefix.
	payload, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	// Circuit breaker pre-check: same gate the deleted adapter applied.
	if c.CircuitBreaker != nil && c.CircuitBreaker.IsOpen() {
		return nil, errors.New("circuit breaker open: provider rate limited after multiple retries")
	}

	// Rate-limit pre-flight + pacing. Cache hints are sourced per call via
	// effectiveCacheHints(in) so concurrent sessions on the same Client
	// don't race on the deprecated shared c.cacheHints field
	// (FU-13 / CW-20260520-0054).
	if c.RateTracker != nil {
		estimatedTokens := len(payload) / 4
		cacheableBytes := 0
		hints := c.effectiveCacheHints(in)
		if len(hints) > 0 {
			cacheableBytes = computeCacheablePrefixBytes(payload, hints)
			estimatedTokens -= cacheableBytes / 4
			if estimatedTokens < 0 {
				estimatedTokens = 0
			}
		}
		avail, limit := c.RateTracker.Remaining()
		slog.Debug("provider: rate budget preflight",
			"provider", "anthropic",
			"estimated_tokens", estimatedTokens,
			"cacheable_bytes", cacheableBytes,
			"payload_bytes", len(payload),
			"available_tpm", avail,
		)
		// If the request alone is larger than the per-minute window, no
		// amount of waiting can fit it — signal the caller to compact
		// instead of repeating waits until the outer ctx deadline fires.
		// Same wrap format the deleted adapter used so the chat-service
		// regex parser at chat_rate_budget_pause.go:rateBudgetEstimateRE
		// still matches.
		if limit > 0 && estimatedTokens > limit {
			slog.Warn("provider: request exceeds per-minute rate budget — signaling caller to compact",
				"provider", "anthropic",
				"estimated_tokens", estimatedTokens,
				"limit", limit,
			)
			return nil, fmt.Errorf("%w: estimated %d tokens vs %d limit",
				llmcontracts.ErrRequestExceedsRateBudget, estimatedTokens, limit)
		}
		if wait := c.RateTracker.WaitTime(estimatedTokens); wait > 0 {
			slog.Info("provider: pacing — waiting for rate-limit budget",
				"provider", "anthropic",
				"wait", wait,
				"estimated_tokens", estimatedTokens,
				"available_tpm", avail,
				"limit_tpm", limit,
			)
			if werr := llmcontracts.PacingWait(ctx, wait, c.OnStatus); werr != nil {
				return nil, fmt.Errorf("anthropic: ctx canceled during rate-limit wait: %w", werr)
			}
		}
	}

	// Per-call options: anthropic-beta header (prompt caching baseline +
	// optional interleaved thinking).
	opts := []option.RequestOption{betaHeaderRequestOption(interleavedThinking)}

	stream := c.sdk.Messages.NewStreaming(ctx, params, opts...)
	if streamErr := stream.Err(); streamErr != nil {
		// NewStreaming surfaces non-OK setup errors via Err() (e.g. auth).
		return nil, translateError(streamErr)
	}

	ch := make(chan llmtypes.StreamEvent, 64)
	go c.runStream(ctx, stream, ch, interleavedThinking)
	return ch, nil
}

// runStream consumes the SDK event stream and emits internal StreamEvents.
// Tracks tool_use accumulators (id/name + partial JSON deltas) and thinking
// accumulators for interleaved thinking. Records input tokens to the rate
// tracker on message_start usage events so the sliding window stays
// accurate across turns.
func (c *Client) runStream(ctx context.Context, stream *ssestream.Stream[sdk.MessageStreamEventUnion], ch chan<- llmtypes.StreamEvent, interleavedThinking bool) {
	defer close(ch)
	defer func() {
		// Close errors here are best-effort: the stream is already drained
		// (or aborted), and surfacing a close error would obscure the real
		// stream error already emitted.
		_ = stream.Close()
	}()

	type toolUseAcc struct {
		id    string
		name  string
		input strings.Builder
	}
	type thinkingAcc struct {
		text      strings.Builder
		signature string
	}

	var currentTool *toolUseAcc
	var currentThinking *thinkingAcc

	for stream.Next() {
		select {
		case <-ctx.Done():
			ch <- llmtypes.StreamEvent{Type: llmtypes.EventError, Error: "context canceled"}
			return
		default:
		}

		ev := stream.Current()
		switch ev.Type {
		case "message_start":
			// Usage on message_start carries input_tokens (and cache
			// creation/read counts). Record input to the rate tracker for
			// sliding-window math, and emit a usage event so chat-service
			// telemetry is fed.
			start := ev.AsMessageStart()
			u := start.Message.Usage
			if c.RateTracker != nil && u.InputTokens > 0 {
				c.RateTracker.Record(int(u.InputTokens))
			}
			ch <- llmtypes.StreamEvent{
				Type: llmtypes.EventUsage,
				Usage: &llmtypes.Usage{
					InputTokens:         int(u.InputTokens),
					CacheCreationTokens: int(u.CacheCreationInputTokens),
					CacheReadTokens:     int(u.CacheReadInputTokens),
				},
			}
			if u.CacheCreationInputTokens > 0 || u.CacheReadInputTokens > 0 {
				slog.Debug("provider: prompt cache",
					"provider", "anthropic",
					"creation", u.CacheCreationInputTokens,
					"read", u.CacheReadInputTokens,
					"input", u.InputTokens,
				)
			}

		case "content_block_start":
			cbs := ev.AsContentBlockStart()
			switch cbs.ContentBlock.Type {
			case "tool_use":
				currentTool = &toolUseAcc{
					id:   cbs.ContentBlock.ID,
					name: cbs.ContentBlock.Name,
				}
				currentThinking = nil
			case "thinking":
				if interleavedThinking {
					currentThinking = &thinkingAcc{
						signature: cbs.ContentBlock.Signature,
					}
				}
				currentTool = nil
			default:
				currentTool = nil
				currentThinking = nil
			}

		case "content_block_delta":
			cbd := ev.AsContentBlockDelta()
			switch cbd.Delta.Type {
			case "text_delta":
				ch <- llmtypes.StreamEvent{
					Type:    llmtypes.EventDelta,
					Content: cbd.Delta.Text,
				}
			case "input_json_delta":
				if currentTool != nil {
					currentTool.input.WriteString(cbd.Delta.PartialJSON)
				}
			case "thinking_delta":
				if interleavedThinking && currentThinking != nil {
					currentThinking.text.WriteString(cbd.Delta.Thinking)
				}
			case "signature_delta":
				if interleavedThinking && currentThinking != nil {
					currentThinking.signature += cbd.Delta.Signature
				}
			}

		case "content_block_stop":
			if currentTool != nil {
				tu := currentTool
				var input map[string]any
				raw := tu.input.String()
				if raw != "" {
					if jerr := json.Unmarshal([]byte(raw), &input); jerr != nil {
						input = map[string]any{"_raw": raw}
					}
				} else {
					input = map[string]any{}
				}
				ch <- llmtypes.StreamEvent{
					Type: llmtypes.EventToolUse,
					ToolUse: &llmtypes.ToolUseBlock{
						ID:    tu.id,
						Name:  tu.name,
						Input: input,
					},
				}
				currentTool = nil
			}
			if interleavedThinking && currentThinking != nil {
				ch <- llmtypes.StreamEvent{
					Type: llmtypes.EventThinking,
					ThinkingBlock: &llmtypes.ThinkingBlock{
						Thinking:  currentThinking.text.String(),
						Signature: currentThinking.signature,
					},
				}
				currentThinking = nil
			}

		case "message_delta":
			md := ev.AsMessageDelta()
			ch <- llmtypes.StreamEvent{
				Type: llmtypes.EventUsage,
				Usage: &llmtypes.Usage{
					OutputTokens: int(md.Usage.OutputTokens),
					StopReason:   string(md.Delta.StopReason),
				},
			}

		case "message_stop":
			ch <- llmtypes.StreamEvent{Type: llmtypes.EventDone}
		}
	}

	if streamErr := stream.Err(); streamErr != nil {
		// Non-terminal error from the SDK (network drop, decode failure,
		// API error mid-stream). Translate to internal error event so the
		// chat loop's error envelope path picks it up.
		ch <- llmtypes.StreamEvent{
			Type:  llmtypes.EventError,
			Error: translateError(streamErr).Error(),
		}
	}
}

