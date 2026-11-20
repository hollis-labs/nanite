package agent

import "github.com/hollis-labs/nanite/internal/permission"

// Gate decides whether an otherwise permitted tool call may proceed when the
// policy asks for approval. The context bounds the time available to decide.
type Gate = permission.Gate

// PermissionRequest identifies the tool and its JSON arguments, along with
// conversation and turn IDs, side-effect metadata, and the reason for asking.
type PermissionRequest = permission.PermissionRequest

// SubtaskOrigin attributes an approval request or question that a subtask
// relayed to its root conversation: the child conversation, its variant and
// goal, and its depth below the root.
type SubtaskOrigin = permission.SubtaskOrigin

// PermissionScope determines how long an approval can satisfy later asks.
type PermissionScope = permission.PermissionScope

// PermissionDecision records the allow or deny result, optional approval
// scope, and reason shown to the host.
type PermissionDecision = permission.PermissionDecision

const (
	// ScopeOnce applies the decision to the current call.
	ScopeOnce = permission.ScopeOnce
	// ScopeConversation applies the decision to the active conversation.
	ScopeConversation = permission.ScopeConversation
)

var (
	// DenyGate denies each approval request.
	DenyGate = permission.DenyGate
	// AllowGate allows each approval request.
	AllowGate = permission.AllowGate
)
