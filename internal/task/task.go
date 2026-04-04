package task

import (
	"fmt"
	"time"
)

// Status represents the lifecycle state of a task.
type Status string

const (
	StatusPending    Status = "pending"
	StatusInProgress Status = "in_progress"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
	StatusCancelled  Status = "cancelled"
)

// Task represents a unit of work in multi-agent orchestration.
type Task struct {
	ID              string            `json:"id"`
	ParentID        string            `json:"parent_id,omitempty"`
	SessionID       string            `json:"session_id"`
	WorkerSessionID string            `json:"worker_session_id,omitempty"`
	Title           string            `json:"title"`
	Description     string            `json:"description,omitempty"`
	Status          Status            `json:"status"`
	AssigneeAgentID string            `json:"assignee_agent_id,omitempty"`
	Result          string            `json:"result,omitempty"`
	Error           string            `json:"error,omitempty"`
	TokensUsed      int               `json:"tokens_used"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	CompletedAt     *time.Time        `json:"completed_at,omitempty"`
}

// IsTerminal returns true if the task is in a final state.
func (t *Task) IsTerminal() bool {
	return t.Status == StatusCompleted || t.Status == StatusFailed || t.Status == StatusCancelled
}

// validTransitions defines which status transitions are allowed.
var validTransitions = map[Status][]Status{
	StatusPending:    {StatusInProgress, StatusCancelled},
	StatusInProgress: {StatusCompleted, StatusFailed, StatusCancelled},
	StatusCompleted:  {},
	StatusFailed:     {StatusPending}, // allow retry
	StatusCancelled:  {},
}

// ValidateTransition checks whether a status transition is allowed.
func ValidateTransition(from, to Status) error {
	allowed, ok := validTransitions[from]
	if !ok {
		return fmt.Errorf("unknown status %q", from)
	}
	for _, s := range allowed {
		if s == to {
			return nil
		}
	}
	return fmt.Errorf("invalid transition from %q to %q", from, to)
}
