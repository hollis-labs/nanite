package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/hollis-labs/go-agent-wrapper/acp"
	"github.com/hollis-labs/nanite/internal/permission"
)

// bestEffortPermissionResponder adapts a provider-originated ACP
// session/request_permission call onto Nanite's existing pending-approval
// engine and allow/deny x once/session vocabulary. It is intentionally nil
// unless both the engine and UI event sink are available; wrapper's nil
// contract then declines safely without inventing a second policy path.
//
// This responder is not an authorization boundary. It runs only when an ACP
// provider voluntarily asks. Nanite's toolclient/RPC gates remain the
// authoritative pre-execution enforcement for host-owned tools.
func bestEffortPermissionResponder(
	sessionID string,
	engine *permission.Engine,
	emit func(*permission.ApprovalRequest),
) acp.BestEffortPermissionRequestResponder {
	if engine == nil || emit == nil {
		return nil
	}
	return func(ctx context.Context, request acp.PermissionRequest) (acp.PermissionSelection, error) {
		toolName := firstNonEmpty(request.ToolCall.Name, request.ToolCall.Title, request.ToolCall.Kind, "acp_tool")
		input := permissionInput(request.ToolCall.RawInput)
		reason := "ACP provider requested permission before an operation"
		if title := strings.TrimSpace(request.ToolCall.Title); title != "" && title != toolName {
			reason += ": " + title
		}

		req := engine.RequestApproval(sessionID, toolName, input, reason)
		emit(req)
		response := engine.WaitForApproval(ctx, req)
		if ctx.Err() != nil {
			// Turn cancellation and close cancel the wrapper-supplied
			// callback context. Do not translate that lifecycle signal into a
			// fresh provider permission choice.
			return acp.PermissionSelection{}, nil
		}
		return selectACPOption(request.Options, response), nil
	}
}

func permissionInput(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err == nil && object != nil {
		return object
	}
	var value any
	if err := json.Unmarshal(raw, &value); err == nil {
		return map[string]any{"value": value}
	}
	return nil
}

func selectACPOption(options []acp.PermissionOption, response permission.ApprovalResponse) acp.PermissionSelection {
	var preferred []acp.PermissionOptionKind
	switch response.Decision {
	case permission.DecisionAllow:
		if response.Scope == permission.ScopeSession {
			// A provider that lacks allow_always can still honor the user's
			// immediate allow decision without broadening it.
			preferred = []acp.PermissionOptionKind{acp.PermissionAllowAlways, acp.PermissionAllowOnce}
		} else {
			// Never turn a one-shot allow into an always grant.
			preferred = []acp.PermissionOptionKind{acp.PermissionAllowOnce}
		}
	case permission.DecisionDeny:
		if response.Scope == permission.ScopeSession {
			preferred = []acp.PermissionOptionKind{acp.PermissionRejectAlways, acp.PermissionRejectOnce}
		} else {
			// Never broaden a one-shot denial into provider-persisted policy.
			preferred = []acp.PermissionOptionKind{acp.PermissionRejectOnce}
		}
	default:
		return acp.PermissionSelection{}
	}

	for _, kind := range preferred {
		for _, option := range options {
			if option.Kind == kind {
				return acp.SelectPermissionOption(option.OptionID)
			}
		}
	}
	return acp.PermissionSelection{}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
