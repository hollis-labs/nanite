package mcp

import (
	"encoding/json"
	"errors"
	"log/slog"

	svcerr "github.com/hollis-labs/go-svcerr"
)

// ServiceErrorResult maps service categories onto MCP's IsError result.
// The structured text carries only the safe message, never the internal cause.
func ServiceErrorResult(err error, context ...any) *ToolResult {
	code, message, field := svcerr.CodeInternal, "internal error", ""
	var typed *svcerr.Error
	if errors.As(err, &typed) && typed != nil {
		code, message, field = typed.Code, typed.Message, typed.Field
	}
	attrs := append([]any{"code", code, "message", message}, context...)
	cause := err
	if typed != nil {
		cause = typed.Err
	}
	if cause != nil {
		attrs = append(attrs, "cause", cause)
	}
	if code == svcerr.CodeInternal || code == svcerr.CodeUnavailable {
		slog.Error("mcp: service operation failed", attrs...)
	} else {
		slog.Warn("mcp: service operation rejected", attrs...)
	}
	body := struct {
		Code    svcerr.Code `json:"code"`
		Message string      `json:"message"`
		Field   string      `json:"field,omitempty"`
	}{code, message, field}
	raw, _ := json.Marshal(body)
	return ErrorResult(string(raw))
}
