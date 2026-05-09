package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"time"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	"github.com/hollis-labs/nanite/internal/chat"
	nllmanthropic "github.com/hollis-labs/nanite/internal/llm/anthropic"
)

// rate_budget_pause is the SSE stream event emitted when a chat turn cannot
// proceed because its estimated request size exceeds the per-minute rate-limit
// window. Glass-6 (CW-20260502-0013, SP-20260502-0001) introduces this event
// to replace the prior `internal_error` / `recovery: refused` pattern that
// ended the SSE stream and forced the user to `/clear`. The session stays
// alive: the SSE connection for the active turn ends cleanly, but no fatal
// error is recorded and the next user message reuses the same session_id.
//
// Naming follows `notify_pause` (chat_notify_pause.go) — both are non-fatal
// recoverable stream events that surface a transient pause to the FE without
// flowing through the `error` envelope path. Frontend rendering is out of
// scope for Glass-6; a follow-up FE ticket picks up the new event.
//
// Payload shape is documented on rateBudgetPausePayload below. retry_after_ms
// comes from go-providers' RateTracker.WaitTime(estimatedTokens). Auto-retry
// is one-shot: if a single wait would let the request fit, the chat loop
// continues transparently after a single auto_retry event. Otherwise a
// second event with suggested_action="user_action_needed" is emitted and
// the turn ends cleanly (no error envelope, session not marked failed).

const (
	rateBudgetPauseEventType = "rate_budget_pause"

	// suggestedActionAutoRetry signals the FE that a transparent retry is in
	// flight after retry_after_ms. UI should keep the spinner / "thinking"
	// indicator alive without surfacing as a failure.
	suggestedActionAutoRetry = "auto_retry"

	// suggestedActionUserActionNeeded signals that the rate-budget headroom
	// is not going to recover within a useful window — the user needs to
	// reduce the request (compact, split, or wait minutes). Turn ends cleanly.
	suggestedActionUserActionNeeded = "user_action_needed"
)

// rateBudgetPausePayload is the JSON shape carried on the SSE Data field for
// rate_budget_pause events. Documented here so frontend follow-up has a
// single canonical reference.
type rateBudgetPausePayload struct {
	SessionID       string   `json:"session_id"`
	EstimatedTokens int      `json:"estimated_tokens"`
	CurrentLimit    int      `json:"current_limit"`
	RetryAfterMS    int64    `json:"retry_after_ms"`
	SuggestedAction string   `json:"suggested_action"`
	RecoveryOptions []string `json:"recovery_options"`
	TriggerKind     string   `json:"trigger_kind"`
}

// rateBudgetEstimateRE matches the format string used by go-providers when it
// wraps ErrRequestExceedsRateBudget: "<sentinel>: estimated %d tokens vs %d
// limit" (see go-providers/provider/anthropic.go in the rate-budget pre-flight
// branch). Parsing the message keeps Glass-6 self-contained — adding typed
// fields to the sentinel is a separate refactor across the provider seam.
var rateBudgetEstimateRE = regexp.MustCompile(`estimated\s+(\d+)\s+tokens\s+vs\s+(\d+)\s+limit`)

// parseRateBudgetEstimate extracts (estimatedTokens, limit) from a wrapped
// ErrRequestExceedsRateBudget error. ok=false when the message shape doesn't
// match (defensive — handler still emits a recoverable event with zeros).
func parseRateBudgetEstimate(err error) (estimated, limit int, ok bool) {
	if err == nil {
		return 0, 0, false
	}
	m := rateBudgetEstimateRE.FindStringSubmatch(err.Error())
	if len(m) != 3 {
		return 0, 0, false
	}
	e, err1 := strconv.Atoi(m[1])
	l, err2 := strconv.Atoi(m[2])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return e, l, true
}

// providerRateTracker returns the RateTracker on the Anthropic provider, or
// nil when prov is a different concrete type (e.g. Gemini, OpenAI) or the
// tracker is unset. Callers must tolerate nil — auto-retry is skipped and
// retry_after_ms is reported as zero.
func providerRateTracker(prov llmcontracts.Provider) *llmcontracts.TokenRateTracker {
	if ap, ok := prov.(*nllmanthropic.Client); ok && ap.RateTracker != nil {
		return ap.RateTracker
	}
	return nil
}

// emitRateBudgetPause writes the rate_budget_pause SSE event to ch. Caller
// owns the channel; nothing here closes it. Marshal failures are logged and
// silently swallowed — emitting nothing is preferable to crashing the loop.
func emitRateBudgetPause(ch chan chat.StreamEvent, payload rateBudgetPausePayload) {
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("chat-service: rate_budget_pause marshal failed", "err", err)
		return
	}
	ch <- chat.StreamEvent{
		Type: rateBudgetPauseEventType,
		Data: string(data),
	}
}

// pauseAndMaybeRetryRateBudget emits a rate_budget_pause event in response
// to a provider-side ErrRequestExceedsRateBudget. On the FIRST pause for the
// active turn it sleeps RateTracker.WaitTime and reports whether the request
// would now fit; subsequent calls go straight to user_action_needed.
//
// Returns shouldRetry=true when the caller should re-run the provider call
// (transparent retry, no further events). Returns false when the turn should
// end cleanly without a fatal error envelope — a second event with
// suggested_action="user_action_needed" was emitted, the session stays
// alive, and the next user message starts a fresh turn.
//
// *attempts is incremented on every call, so the loopState counter survives
// across both branches that invoke this helper (recovery-refused at chat
// loop ~line 905 and failed-after-retry at ~line 939).
func (s *chatServiceImpl) pauseAndMaybeRetryRateBudget(
	ctx context.Context,
	sessionID string,
	prov llmcontracts.Provider,
	ch chan chat.StreamEvent,
	err error,
	triggerKind string,
	attempts *int,
) (shouldRetry bool) {
	estimated, limit, parsed := parseRateBudgetEstimate(err)
	if !parsed {
		// Defensive: emit the recoverable event with zeros rather than fall
		// back to the fatal-error path. Future provider seams that produce
		// rate-budget errors with different formatting will surface here.
		slog.Warn("chat-service: rate_budget_pause unable to parse estimate from error",
			"session_id", sessionID, "err", err)
	}

	rt := providerRateTracker(prov)
	var retryAfter time.Duration
	if rt != nil && estimated > 0 {
		retryAfter = rt.WaitTime(estimated)
	}

	first := attempts != nil && *attempts == 0
	if attempts != nil {
		*attempts++
	}

	if first {
		emitRateBudgetPause(ch, rateBudgetPausePayload{
			SessionID:       sessionID,
			EstimatedTokens: estimated,
			CurrentLimit:    limit,
			RetryAfterMS:    retryAfter.Milliseconds(),
			SuggestedAction: suggestedActionAutoRetry,
			RecoveryOptions: []string{"wait", "compact_more", "user_split_message"},
			TriggerKind:     triggerKind,
		})
		slog.Info("chat-service: rate_budget_pause emitted (auto_retry)",
			"session_id", sessionID, "estimated_tokens", estimated, "limit", limit,
			"retry_after_ms", retryAfter.Milliseconds(), "trigger_kind", triggerKind)

		if retryAfter > 0 && rt != nil {
			timer := time.NewTimer(retryAfter)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				slog.Info("chat-service: rate_budget_pause canceled by ctx",
					"session_id", sessionID, "err", ctx.Err())
				return false
			case <-timer.C:
			}
		}

		// Recheck: would the request fit now?
		if rt != nil && estimated > 0 && rt.WaitTime(estimated) == 0 {
			slog.Info("chat-service: rate_budget_pause auto_retry — budget freed",
				"session_id", sessionID, "estimated_tokens", estimated, "limit", limit)
			return true
		}
	}

	// Either we already paused once for this turn, or the auto-retry window
	// would not clear enough headroom. Emit user_action_needed and tell the
	// caller to end the turn cleanly without a fatal envelope.
	emitRateBudgetPause(ch, rateBudgetPausePayload{
		SessionID:       sessionID,
		EstimatedTokens: estimated,
		CurrentLimit:    limit,
		RetryAfterMS:    retryAfter.Milliseconds(),
		SuggestedAction: suggestedActionUserActionNeeded,
		RecoveryOptions: []string{"compact_more", "user_split_message"},
		TriggerKind:     triggerKind,
	})
	attemptsSeen := 0
	if attempts != nil {
		attemptsSeen = *attempts
	}
	slog.Info("chat-service: rate_budget_pause emitted (user_action_needed)",
		"session_id", sessionID, "estimated_tokens", estimated, "limit", limit,
		"retry_after_ms", retryAfter.Milliseconds(), "trigger_kind", triggerKind,
		"attempts", attemptsSeen)
	return false
}

// IsRateBudgetExceeded reports whether err wraps the go-llm-contracts
// rate-budget sentinel. Provided for callers that have not already
// classified the error (e.g. tests).
func IsRateBudgetExceeded(err error) bool {
	return errors.Is(err, llmcontracts.ErrRequestExceedsRateBudget)
}

// asWrappedRateBudgetError synthesizes a wrapped error matching the
// wrapper's format string for tests. Not used by production code; kept
// here so the parser and the producer are colocated.
func asWrappedRateBudgetError(estimated, limit int) error {
	return fmt.Errorf("%w: estimated %d tokens vs %d limit",
		llmcontracts.ErrRequestExceedsRateBudget, estimated, limit)
}
