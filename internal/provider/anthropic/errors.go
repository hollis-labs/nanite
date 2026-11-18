package anthropic

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	llmcontracts "github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/nanite/internal/provider/station"
)

var (
	// ErrTransient wraps retryable API errors.
	ErrTransient = errors.New("anthropic: transient provider error")
	// ErrAuthentication wraps API authentication and authorization errors.
	ErrAuthentication = errors.New("anthropic: authentication error")
)

// translateError preserves typed gateway errors and classifies provider
// failures for callers deciding whether to retry or compact.
func translateError(err error) error {
	if err == nil {
		return nil
	}

	var apiErr *sdk.Error
	if !errors.As(err, &apiErr) {
		return err
	}

	if gwErr := station.Classify(apiErr.StatusCode, string(apiErr.Type()), apiErr.RawJSON(), apiErr); gwErr != nil {
		return gwErr
	}

	switch apiErr.StatusCode {
	case http.StatusTooManyRequests:
		// Distinguish an oversized request from a transient throttle.
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
