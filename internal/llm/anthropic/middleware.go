package anthropic

import (
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"

	"github.com/anthropics/anthropic-sdk-go/option"
	llmcontracts "github.com/hollis-labs/go-llm-contracts"
)

// Anthropic rate-limit response headers — match the names parsed by the
// deleted hand-rolled adapter so behavior is preserved across the
// migration. Header names, not credentials — gosec G101 false-positive.
const (
	headerRateLimitInputTokens          = "x-ratelimit-limit-input-tokens"          //nolint:gosec // header name
	headerRateLimitRemainingInputTokens = "x-ratelimit-remaining-input-tokens"      //nolint:gosec // header name
	headerRateLimitResetInputTokens     = "x-ratelimit-reset-input-tokens"          //nolint:gosec // header name
)

// rateAwareMiddleware returns an option.Middleware that:
//
//  1. Parses Anthropic's x-ratelimit-* response headers and feeds the limit
//     into the TokenRateTracker (calibration). Marks calibrated=true on first
//     successful read so RateLimitTPM can return the observed value rather
//     than the seeded default.
//  2. Drives the CircuitBreaker — RecordSuccess on 2xx, RecordFailure on
//     429 or 5xx. Anthropic's 4xx-other (e.g. 400 invalid request) is a
//     caller-fault, not a transport failure, so it does not record breaker
//     state.
//  3. Returns the response untouched — error translation (429 →
//     ErrRequestExceedsRateBudget) happens in errors.go when the SDK call
//     surfaces an *sdk.Error to the wrapper. The middleware only needs to
//     update calibration + breaker state; the SDK takes care of body decode
//     and error envelope.
//
// Transport-layer errors (network failure, ctx cancel) propagate through
// next(req) as-is and increment breaker failure once — same as the deleted
// adapter recorded a failure for each non-OK exit out of the retry loop.
func rateAwareMiddleware(rt *llmcontracts.TokenRateTracker, cb *llmcontracts.CircuitBreaker, calibrated *atomic.Bool) option.Middleware {
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		resp, err := next(req)
		if err != nil {
			if cb != nil {
				cb.RecordFailure()
			}
			return resp, err
		}

		// Calibrate the rate tracker from the limit header. Same name and
		// semantics the deleted adapter consumed: log only on first read or
		// on a real change in observed limit.
		if rt != nil {
			calibrateRateTracker(rt, resp, calibrated)
		}

		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			if cb != nil {
				cb.RecordFailure()
			}
		case resp.StatusCode >= 500:
			if cb != nil {
				cb.RecordFailure()
			}
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			if cb != nil {
				cb.RecordSuccess()
			}
			// 4xx-other (e.g. 400 invalid request, 401 auth) is treated as
			// a caller fault and does not move breaker state — matches the
			// previous adapter's behavior where only retryable errors fed
			// the breaker.
		}
		return resp, nil
	}
}

// calibrateRateTracker reads Anthropic rate-limit headers and updates the
// tracker. Logs only when the calibrated limit actually changes (first
// calibration or a real tier transition); same-value re-calibrations are
// silent so the log signal stays meaningful.
func calibrateRateTracker(rt *llmcontracts.TokenRateTracker, resp *http.Response, calibrated *atomic.Bool) {
	limitStr := resp.Header.Get(headerRateLimitInputTokens)
	if limitStr == "" {
		return
	}
	newLimit, err := strconv.Atoi(limitStr)
	if err != nil || newLimit <= 0 {
		return
	}
	// Mark calibrated on first successful header read. Re-setting on every
	// calibration is fine — atomic.Bool.Store is cheap and idempotent.
	if calibrated != nil {
		calibrated.Store(true)
	}
	_, oldLimit := rt.Remaining()
	if oldLimit == newLimit {
		return
	}
	rt.UpdateLimit(newLimit)

	remainingTPM := 0
	if v, perr := strconv.Atoi(resp.Header.Get(headerRateLimitRemainingInputTokens)); perr == nil {
		remainingTPM = v
	}
	resetAt := resp.Header.Get(headerRateLimitResetInputTokens)
	slog.Info("provider: rate limit calibrated",
		"provider", "anthropic",
		"old_limit_tpm", oldLimit,
		"new_limit_tpm", newLimit,
		"remaining_tpm", remainingTPM,
		"reset_at", resetAt,
	)
}
