package chat

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// ErrorCode classifies chat errors for structured error reporting.
type ErrorCode string

const (
	ErrorCodeRateLimit     ErrorCode = "rate_limit"
	ErrorCodeToolError     ErrorCode = "tool_error"
	ErrorCodeProviderError ErrorCode = "provider_error"
	ErrorCodeInternal      ErrorCode = "internal_error"
)

// ChatError is a structured error payload sent via SSE error events.
type ChatError struct {
	Code      ErrorCode              `json:"code"`
	Message   string                 `json:"message"`
	Details   map[string]interface{} `json:"details,omitempty"`
	Timestamp string                 `json:"timestamp"`
}

// NewChatError creates a ChatError with the current timestamp.
func NewChatError(code ErrorCode, message string, details map[string]interface{}) ChatError {
	return ChatError{
		Code:      code,
		Message:   message,
		Details:   details,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
}

// JSON returns the JSON-encoded string of the ChatError.
func (ce ChatError) JSON() string {
	data, _ := json.Marshal(ce)
	return string(data)
}

// classifyError inspects an error string and returns the appropriate ErrorCode.
func classifyError(err error) ErrorCode {
	msg := err.Error()
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "429") || strings.Contains(lower, "rate") {
		return ErrorCodeRateLimit
	}
	return ErrorCodeProviderError
}

// errorEvent builds a StreamEvent with a structured ChatError payload.
func errorEvent(code ErrorCode, userMessage string, details map[string]interface{}) StreamEvent {
	ce := NewChatError(code, userMessage, details)
	return StreamEvent{
		Type:            "error",
		Error:           userMessage,
		StructuredError: &ce,
	}
}

// errorGiphyQueries is a pool of fun error-themed Giphy search terms.
var errorGiphyQueries = []string{
	"computer error funny",
	"it works on my machine",
	"this is fine fire",
	"confused computer",
	"panic button",
	"oops mistake",
	"frustrated programmer",
}

// errorEnvelopeData is the payload embedded in an error-report envelope.
type errorEnvelopeData struct {
	Code       string                 `json:"code"`
	Message    string                 `json:"message"`
	Details    map[string]interface{} `json:"details,omitempty"`
	GiphyQuery string                `json:"giphy_query"`
	Timestamp  string                `json:"timestamp"`
}

// buildErrorEnvelope creates a JSON string for an error-report conduit-envelope block.
func buildErrorEnvelope(code ErrorCode, message string, details map[string]interface{}) string {
	data := errorEnvelopeData{
		Code:       string(code),
		Message:    message,
		Details:    details,
		GiphyQuery: errorGiphyQueries[rand.Intn(len(errorGiphyQueries))],
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}
	envelope := map[string]interface{}{
		"kind":    "envelope",
		"version": 1,
		"type":    "error-report",
		"data":    data,
	}
	out, _ := json.Marshal(envelope)
	return string(out)
}

// errorEnvelopeDelta returns a StreamEvent delta containing an error envelope block.
func errorEnvelopeDelta(code ErrorCode, message string, details map[string]interface{}) StreamEvent {
	envJSON := buildErrorEnvelope(code, message, details)
	return StreamEvent{
		Type:    "delta",
		Content: fmt.Sprintf("\n\n```conduit-envelope\n%s\n```", envJSON),
	}
}
