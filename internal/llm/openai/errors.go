package openai

import (
	"errors"
	"fmt"
	"net/http"

	sdk "github.com/openai/openai-go"
)

// errEmptyResponse is returned when the API replies 200 with no usable
// choices/data — typically indicates a transient API hiccup.
var errEmptyResponse = errors.New("openai: empty response")

// translateError wraps SDK errors into nanite-friendly diagnostic shapes.
// The current OpenAI provider in nanite did not classify into specific
// sentinels (no provider.ErrRateLimit / ErrRequestExceedsRateBudget paths
// for OpenAI); we keep that shape and only annotate the message with the
// upstream HTTP status when available.
//
// Adding sentinels (e.g. for 429-driven backoff) is captured as follow-up
// `followups.nanite.cw_20260508_0012.openai_rate_budget_parity`.
func translateError(err error) error {
	if err == nil {
		return nil
	}
	var apierr *sdk.Error
	if errors.As(err, &apierr) {
		return fmt.Errorf("openai: %s %d %s: %s",
			http.StatusText(apierr.StatusCode), apierr.StatusCode, apierr.Code, apierr.Message)
	}
	return fmt.Errorf("openai: %w", err)
}
