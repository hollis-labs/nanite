package provider

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RetryConfig controls exponential backoff behaviour.
type RetryConfig struct {
	MaxRetries   int           // default 3
	InitialDelay time.Duration // default 1s
	MaxDelay     time.Duration // default 8s
	Multiplier   float64       // default 2.0
}

// DefaultRetryConfig returns the standard retry configuration.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:   3,
		InitialDelay: 1 * time.Second,
		MaxDelay:     8 * time.Second,
		Multiplier:   2.0,
	}
}

// APIError represents an HTTP error from an LLM API that may be retryable.
type APIError struct {
	StatusCode int
	Message    string
	RetryAfter time.Duration // parsed from Retry-After header, 0 if absent
}

func (e *APIError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("API error %d: %s (retry-after: %s)", e.StatusCode, e.Message, e.RetryAfter)
	}
	return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Message)
}

// RetryableStatusCode returns true for status codes that should be retried.
func RetryableStatusCode(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests ||
		statusCode == 529 || // Anthropic overloaded
		(statusCode >= 500 && statusCode < 600)
}

// IsRetryableError checks if an error is a retryable APIError.
func IsRetryableError(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return RetryableStatusCode(apiErr.StatusCode)
	}
	return false
}

// ParseRetryAfter parses the Retry-After header value.
// It supports seconds (integer) and HTTP-date formats.
func ParseRetryAfter(header string) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}

	// Try integer seconds first.
	if secs, err := strconv.Atoi(header); err == nil {
		return time.Duration(secs) * time.Second
	}

	// Try HTTP-date format.
	for _, layout := range []string{
		time.RFC1123,
		time.RFC1123Z,
		time.RFC850,
		time.ANSIC,
	} {
		if t, err := time.Parse(layout, header); err == nil {
			d := time.Until(t)
			if d < 0 {
				return 0
			}
			return d
		}
	}

	return 0
}

// BackoffDelay calculates the delay for a given attempt using exponential backoff.
// If retryAfter is non-zero it is used instead (but capped at MaxDelay).
func (c RetryConfig) BackoffDelay(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		if retryAfter > c.MaxDelay {
			return c.MaxDelay
		}
		return retryAfter
	}
	delay := time.Duration(float64(c.InitialDelay) * math.Pow(c.Multiplier, float64(attempt)))
	if delay > c.MaxDelay {
		delay = c.MaxDelay
	}
	return delay
}

// StatusCallback is called during retries to report status to the caller.
type StatusCallback func(message string)
