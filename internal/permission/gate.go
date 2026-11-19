package permission

import (
	"context"
	"encoding/json"
)

// Gate handles permission requests requiring human input.
type Gate interface {
	Decide(context.Context, PermissionRequest) (PermissionDecision, error)
}

// PermissionRequest describes a tool call awaiting approval.
type PermissionRequest struct {
	ID, ConversationID, TurnID, Tool string
	Input                            json.RawMessage
	ReadOnly, Destructive            bool
	Reason                           string
}

// PermissionScope determines how long an approval applies.
type PermissionScope uint8

const (
	// ScopeOnce applies the decision to the current call.
	ScopeOnce PermissionScope = iota
	// ScopeConversation applies the decision to the active conversation.
	ScopeConversation
)

// PermissionDecision records whether the call may proceed.
type PermissionDecision struct {
	Allow  bool
	Scope  PermissionScope
	Reason string
}

type staticGate bool

func (g staticGate) Decide(context.Context, PermissionRequest) (PermissionDecision, error) {
	if g {
		return PermissionDecision{Allow: true, Reason: "approved by host gate"}, nil
	}
	return PermissionDecision{Reason: "no interactive approval gate configured"}, nil
}

var (
	// DenyGate denies all requests for approval.
	DenyGate Gate = staticGate(false)
	// AllowGate approves all requests for approval.
	AllowGate Gate = staticGate(true)
)
