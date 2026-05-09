package context

import (
	"errors"
	"fmt"
	"testing"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
)

func TestIsContextOverflow_Sentinel(t *testing.T) {
	if !IsContextOverflow(ErrContextOverflow) {
		t.Fatal("sentinel should match itself")
	}
	wrapped := fmt.Errorf("stream start failed: %w", ErrContextOverflow)
	if !IsContextOverflow(wrapped) {
		t.Fatal("wrapped sentinel should match")
	}
}

func TestIsContextOverflow_Nil(t *testing.T) {
	if IsContextOverflow(nil) {
		t.Fatal("nil should not match")
	}
}

func TestIsContextOverflow_ProviderPhrases(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{"anthropic prompt too long", "400 prompt is too long: 224213 tokens > 200000 maximum", true},
		{"anthropic input too long stream", "input is too long for requested model", true},
		{"openai code", `{"error":{"message":"context_length_exceeded","type":"..."}}`, true},
		{"openai human", "This model's maximum context length is 128000 tokens", true},
		{"gemini tokens", "too many tokens in request", true},
		{"gemini resource exhausted", "The resource has been exhausted (tokens).", true},
		{"ollama", "exceeded context window of 8192 tokens", true},
		{"ollama token limit", "exceeded token limit for model", true},
		{"rate limit", "rate_limit_exceeded", false},
		{"auth", "invalid_api_key", false},
		{"generic timeout", "context deadline exceeded", false}, // NB: not a prompt-overflow; don't retry
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsContextOverflowMessage(tc.msg); got != tc.want {
				t.Fatalf("IsContextOverflowMessage(%q) = %v, want %v", tc.msg, got, tc.want)
			}
			if got := IsContextOverflow(errors.New(tc.msg)); got != tc.want {
				t.Fatalf("IsContextOverflow(err(%q)) = %v, want %v", tc.msg, got, tc.want)
			}
		})
	}
}

func TestIsContextOverflow_CaseInsensitive(t *testing.T) {
	if !IsContextOverflowMessage("PROMPT IS TOO LONG") {
		t.Fatal("classifier should be case-insensitive")
	}
}

// TestIsCompactRecoverable covers the unified predicate used by the chat
// loop to decide whether to compact-and-retry. CW-20260418-0099.
func TestIsCompactRecoverable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"random error", errors.New("some unrelated failure"), false},
		{"rate budget sentinel", llmcontracts.ErrRequestExceedsRateBudget, true},
		{"wrapped rate budget", fmt.Errorf("stream start: %w", llmcontracts.ErrRequestExceedsRateBudget), true},
		{"context overflow sentinel", ErrContextOverflow, true},
		{"wrapped context overflow", fmt.Errorf("x: %w", ErrContextOverflow), true},
		{"anthropic prompt too long text", errors.New("400 prompt is too long: 224213 tokens > 200000"), true},
		{"openai context length text", errors.New("context_length_exceeded"), true},
		{"rate limit hit text", errors.New("rate_limit_exceeded"), false},
		{"auth error", errors.New("invalid_api_key"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsCompactRecoverable(tc.err); got != tc.want {
				t.Fatalf("IsCompactRecoverable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
