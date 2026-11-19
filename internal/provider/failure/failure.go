// Package failure defines the provider errors consumed by the turn loop.
package failure

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
)

// ErrEmptyResponse is the error every adapter's Complete returns, wrapped,
// when the model produced no text: no choices, no text blocks, or only
// whitespace. See the provider package doc for the full contract.
var ErrEmptyResponse = errors.New("provider: model returned an empty response")

type Class string

const (
	Retryable Class = "retryable"
	Overflow  Class = "overflow"
	Auth      Class = "auth"
	Fatal     Class = "fatal"
)

type Error struct {
	Class      Class
	RetryAfter time.Duration
	Cause      error
}

func (e *Error) Error() string { return e.Cause.Error() }
func (e *Error) Unwrap() error { return e.Cause }

func HTTP(status int, code string, response *http.Response, cause error) *Error {
	class := Fatal
	switch {
	case status == 401 || status == 403 || code == "authentication_error" || code == "permission_error" || code == "invalid_api_key":
		class = Auth
	case status == 413 || code == "context_length_exceeded" || code == "request_too_large" || code == "prompt_too_long":
		class = Overflow
	case status == 408 || status == 429 || status >= 500 || code == "overloaded_error" || code == "rate_limit_error" || code == "rate_limit_exceeded" || code == "server_error" || code == "api_error":
		class = Retryable
	}
	var delay time.Duration
	if response != nil && class == Retryable {
		value := response.Header.Get("Retry-After")
		if seconds, err := strconv.ParseInt(value, 10, 32); err == nil && seconds > 0 {
			delay = time.Duration(seconds) * time.Second
		} else if date, err := http.ParseTime(value); err == nil {
			delay = max(0, time.Until(date))
		}
	}
	return &Error{Class: class, RetryAfter: delay, Cause: cause}
}

func Transport(err error) error {
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return &Error{Class: Retryable, Cause: err}
	}
	return err
}

// Event preserves error classification across the external stream contract,
// whose terminal failure carries text only. Content carries adapter metadata.
func Event(err error) llmtypes.StreamEvent {
	ev := llmtypes.StreamEvent{Type: llmtypes.EventError, Error: err.Error()}
	var classified *Error
	if errors.As(err, &classified) {
		ev.Content = "nanite-provider:" + string(classified.Class) + ":" + classified.RetryAfter.String()
	}
	return ev
}

func FromEvent(ev llmtypes.StreamEvent) error {
	cause := errors.New(ev.Error)
	value, ok := strings.CutPrefix(ev.Content, "nanite-provider:")
	if !ok {
		return cause
	}
	name, delay, _ := strings.Cut(value, ":")
	class := Class(name)
	switch class {
	case Retryable, Overflow, Auth, Fatal:
		timeout, _ := time.ParseDuration(delay)
		return &Error{Class: class, RetryAfter: timeout, Cause: cause}
	default:
		return cause
	}
}
