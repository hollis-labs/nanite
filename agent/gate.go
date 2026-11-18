package agent

import "github.com/hollis-labs/nanite/internal/permission"

// Gate is defined by the tool permission boundary.
type Gate = permission.Gate

// PermissionRequest is defined by the tool permission boundary.
type PermissionRequest = permission.PermissionRequest

// PermissionScope is defined by the tool permission boundary.
type PermissionScope = permission.PermissionScope

// PermissionDecision is defined by the tool permission boundary.
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
