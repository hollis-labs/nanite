package anthropic

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	llmcontracts "github.com/hollis-labs/go-llm-contracts"
)

// Wrapper-local sentinels for non-rate-budget transient categories. The
// previous go-providers/provider/anthropic.go exported provider.ErrRateLimit
// for HTTP 429s that were NOT pre-flighted as oversized; that sentinel was
// deleted in Wave 1 and is not in go-llm-contracts.
//
// chat_rate_budget_pause.go only matches against
// llmcontracts.ErrRequestExceedsRateBudget, and the chat-loop's
// IsCompactRecoverable predicate only matches that sentinel + the context-
// overflow message tokens. Other transient categories surface as plain
// wrapped errors today; the wrapper preserves that shape.
var (
	// ErrTransient is a generic wrapper for retryable HTTP errors (5xx,
	// network blips, gateway timeouts) that nanite's chat loop does not
	// classify specially today. Exposed for tests and future call-sites.
	ErrTransient = errors.New("anthropic: transient provider error")

	// ErrAuthentication wraps 401/403 responses from the API. Currently
	// chat-service surfaces these as opaque error envelopes; the sentinel
	// is here so future callers can errors.Is for retry-vs-bail decisions.
	ErrAuthentication = errors.New("anthropic: authentication error")
)

// translateError converts an SDK error into the shape nanite call-sites
// expect. The two load-bearing categories are:
//
//   - 429 "request_too_large" / Anthropic's rate-budget refusal: wrap with
//     llmcontracts.ErrRequestExceedsRateBudget so chat_rate_budget_pause's
//     errors.Is gate fires and the rate_budget_pause SSE event is emitted.
//   - All other 4xx/5xx: return as-is (wrapped with status code) so the
//     existing error envelope path in chat-service surfaces a useful
//     message to the user. Mirrors the deleted adapter's APIError surface.
//
// For non-API errors (network failure, ctx cancel) the SDK returns the
// underlying error; we surface as-is.
func translateError(err error) error {
	if err == nil {
		return nil
	}

	var apiErr *sdk.Error
	if !errors.As(err, &apiErr) {
		return err
	}

	switch apiErr.StatusCode {
	case http.StatusTooManyRequests:
		// Anthropic uses "rate_limit_error" or "request_too_large" as the
		// error type. Both translate to the rate-budget sentinel —
		// chat_rate_budget_pause's pause+retry path is the right response
		// for either. Match both string forms and the typed Type() field.
		body := apiErr.RawJSON()
		if isRequestTooLarge(apiErr, body) {
			return fmt.Errorf("%w: %s", llmcontracts.ErrRequestExceedsRateBudget, apiErr.Error())
		}
		// Generic 429: still treat as rate-budget; pause+retry is the only
		// sane response. The sentinel match is what gates the recoverable
		// path, so wrap consistently.
		return fmt.Errorf("%w: %s", llmcontracts.ErrRequestExceedsRateBudget, apiErr.Error())

	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: %s", ErrAuthentication, apiErr.Error())

	default:
		if apiErr.StatusCode >= 500 {
			return fmt.Errorf("%w: %s", ErrTransient, apiErr.Error())
		}
		return apiErr
	}
}

// isRequestTooLarge inspects an Anthropic API error to decide whether the
// 429 should classify as "request exceeds rate budget" specifically (rather
// than a transient throttle). Both forms surface to the same sentinel so
// the chat-loop pause+retry path fires either way; the helper is here for
// future shaping if needed.
func isRequestTooLarge(apiErr *sdk.Error, rawBody string) bool {
	if apiErr == nil {
		return false
	}
	if string(apiErr.Type()) == "request_too_large" {
		return true
	}
	if strings.Contains(strings.ToLower(rawBody), "request_too_large") {
		return true
	}
	return false
}
