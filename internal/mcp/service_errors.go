package mcp

import (
	"encoding/json"
	"errors"
	"log/slog"

	svcerr "github.com/hollis-labs/go-svcerr"
)

// ServiceErrorResult maps service categories onto MCP's IsError result.
// The structured text carries only the safe message, never the internal cause.
func ServiceErrorResult(err error) *ToolResult {
	code, message, field := svcerr.CodeInternal, "internal error", ""
	var typed *svcerr.Error
	if errors.As(err, &typed) && typed != nil {
		code, message, field = typed.Code, typed.Message, typed.Field
	}
	if typed != nil && typed.Err != nil {
		if typed.Code == svcerr.CodeInternal || typed.Code == svcerr.CodeUnavailable {
			slog.Error("mcp: service operation failed", "code", typed.Code, "cause", typed.Err)
		} else {
			slog.Warn("mcp: service operation rejected", "code", typed.Code, "cause", typed.Err)
		}
	} else if typed == nil {
		slog.Error("mcp: service operation failed", "cause", err)
	}
	body := struct {
		Code    svcerr.Code `json:"code"`
		Message string      `json:"message"`
		Field   string      `json:"field,omitempty"`
	}{code, message, field}
	raw, _ := json.Marshal(body)
	return ErrorResult(string(raw))
}
