package chat

import (
	"encoding/json"
)

// DelegationRequest describes a task to delegate to a worker agent session.
type DelegationRequest struct {
	ParentSessionID string `json:"parent_session_id"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	AgentID         string `json:"agent_id,omitempty"`   // worker agent (defaults to parent's agent)
	Mode            string `json:"mode,omitempty"`       // worker mode (defaults to "default")
	Model           string `json:"model,omitempty"`      // LLM model (defaults to parent's model)
	WorkspaceID     string `json:"workspace_id,omitempty"`
}

// DelegationResult holds the outcome of a delegated task.
type DelegationResult struct {
	WorkerSessionID string `json:"worker_session_id"`
	TaskID          string `json:"task_id,omitempty"`
	Content         string `json:"content"`
	TokensUsed      int    `json:"tokens_used"`
	Success         bool   `json:"success"`
	Error           string `json:"error,omitempty"`
}

// DelegationStatus returns metadata about a delegation (for UI display).
type DelegationStatus struct {
	ParentSessionID string          `json:"parent_session_id"`
	Workers         []WorkerStatus  `json:"workers"`
	Plan            json.RawMessage `json:"plan,omitempty"`
}

// WorkerStatus tracks the state of a worker session.
type WorkerStatus struct {
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
	Status    string `json:"status"` // running, completed, failed
	Output    string `json:"output,omitempty"`
}
