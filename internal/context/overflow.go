package context

import (
	"errors"
	"strings"
)

// ErrContextOverflow is a sentinel reported by nanite when a provider returns
// a "context length exceeded" / "prompt too long" error, either at stream
// start or mid-stream. Callers that see this can run the compaction pipeline
// synchronously and retry once.
//
// Each provider adapter surfaces the raw error text differently; the
// IsContextOverflow classifier normalises those shapes into a single boolean.
// The sentinel is returned by the recovery helper so downstream callers can
// branch with errors.Is without re-matching strings.
//
// Folded from BLG-20260410-003 (S3b plan §T9).
var ErrContextOverflow = errors.New("provider context overflow")

// overflowTokens lists case-insensitive substrings that every major provider
// uses in their native 4xx or stream-error message for "your prompt + tools +
// conversation exceeded the context window". Anthropic, OpenAI(-compatible),
// Gemini, and Ollama all surface some phrase in this list at the time of
// writing (2026-04).
var overflowTokens = []string{
	"context_length_exceeded",       // OpenAI error code
	"context length exceeded",       // OpenAI human text
	"maximum context length",        // OpenAI long form
	"prompt is too long",            // Anthropic
	"input is too long",             // Anthropic stream error
	"input length exceeds",          // Anthropic variant
	"context window",                // Gemini / generic
	"context window exceeded",       // generic
	"token limit",                   // Ollama
	"exceeds.*tokens",               // Ollama / generic (literal "exceeds" near "tokens")
	"too many tokens",               // Gemini
	"resource has been exhausted",   // Gemini RESOURCE_EXHAUSTED — ambiguous but worth retrying
}

// IsContextOverflow reports whether err looks like a provider context-overflow
// error. Returns true for the sentinel, and for any error whose message
// matches a known overflow phrase.
func IsContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrContextOverflow) {
		return true
	}
	return IsContextOverflowMessage(err.Error())
}

// IsContextOverflowMessage applies the same matching to a raw error string —
// used when the failure surfaces as a StreamEvent{Type:"error", Error:"..."}
// rather than a Go error value.
func IsContextOverflowMessage(msg string) bool {
	if msg == "" {
		return false
	}
	lower := strings.ToLower(msg)
	for _, tok := range overflowTokens {
		if strings.Contains(lower, tok) {
			return true
		}
	}
	return false
}
