package chat

import (
	"encoding/json"
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
