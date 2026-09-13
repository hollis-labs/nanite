package chat

import (
	"errors"
	"strings"
	"testing"
)

func TestProviderRequestRejection(t *testing.T) {
	for _, tc := range []struct {
		raw      string
		rejected bool
	}{
		{"openai: Bad Request 400 unsupported_value: reasoning_effort does not support none", true},
		{"provider HTTP 422: malformed tool schema", true},
		{"invalid_request_error: unknown model", true},
		{"openai: rate limit 429", false},
		{"provider returned 503 server error", false},
		{"read timeout after 400 milliseconds", false},
	} {
		if got := IsProviderRequestRejected(errors.New(tc.raw)); got != tc.rejected {
			t.Errorf("%s: rejected=%v, want %v", tc.raw, got, tc.rejected)
		}
	}
	failure := ProviderFailure(errors.New("unsupported_value: reasoning_effort"), "gpt-6-astra", "message")
	if !strings.Contains(failure.Message, "Choose another model") || failure.Details["request_rejected"] != true {
		t.Fatalf("missing recoverable request explanation: %+v", failure)
	}
}
