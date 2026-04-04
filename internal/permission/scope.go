package permission

// Scope determines how long a permission grant persists.
type Scope string

const (
	// ScopeOnce grants permission for this single invocation only.
	ScopeOnce Scope = "once"
	// ScopeSession grants permission for the remainder of this session.
	ScopeSession Scope = "session"
	// ScopeProject writes permission to .nanite/permissions.yaml.
	ScopeProject Scope = "project"
)
