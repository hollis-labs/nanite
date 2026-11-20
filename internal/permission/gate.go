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
//
// A subtask's request is relayed to its root conversation: ConversationID is
// then the root's, and Subtask names the child that asked.
type PermissionRequest struct {
	ID, ConversationID, TurnID, Tool string
	Input                            json.RawMessage
	ReadOnly, Destructive            bool
	Reason                           string
	Subtask                          *SubtaskOrigin `json:",omitempty"`
}

// SubtaskOrigin attributes a relayed approval or question to the subtask that
// asked: its child conversation, variant, goal and depth below the root.
type SubtaskOrigin struct {
	ChildID string `json:"child_id"`
	Variant string `json:"variant"`
	Goal    string `json:"goal"`
	Depth   int    `json:"depth"`
	// CallID is the root conversation's subtask tool call that started this
	// subtask's chain, so a client can attach the ask to it.
	CallID string `json:"call_id,omitempty"`
	// Dispatched marks a subtask chain the host started with Dispatch rather
	// than the model with a subtask call.
	Dispatched bool `json:"dispatched,omitempty"`
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
	// Human is set by a gate when a person gave this answer, as opposed to a
	// default, a timeout or a policy. A subtask reports status "denied" only
	// for a refusal a person gave.
	Human bool
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
