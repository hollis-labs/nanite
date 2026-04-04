package worker

import (
	"context"
	"time"
)

// Type distinguishes worker execution modes.
type Type string

const (
	// TypeFull spawns a complete agent session with its own chat loop,
	// context window, and tool set.
	TypeFull Type = "full"

	// TypeLight executes a single tool call or batch without an LLM loop.
	TypeLight Type = "light"
)

// Status tracks the lifecycle of a worker.
type Status string

const (
	StatusSpawning  Status = "spawning"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// Worker represents an active worker in the orchestration system.
type Worker struct {
	ID              string             `json:"id"`
	Type            Type               `json:"type"`
	ParentSessionID string             `json:"parent_session_id"`
	SessionID       string             `json:"session_id,omitempty"`   // for full workers
	TaskID          string             `json:"task_id,omitempty"`      // linked task
	AgentID         string             `json:"agent_id"`
	Status          Status             `json:"status"`
	WorktreePath    string             `json:"worktree_path,omitempty"`
	CreatedAt       time.Time          `json:"created_at"`
	cancel          context.CancelFunc `json:"-"`
}

// SpawnRequest describes a full worker session to spawn.
type SpawnRequest struct {
	ParentSessionID string
	Title           string
	Description     string
	AgentID         string // optional, defaults to parent's agent
	Mode            string // optional, defaults to "default"
	Model           string // optional, defaults to parent's model
	WorkspaceID     string // optional
	Isolation       string // "worktree" triggers git worktree creation
	TaskID          string // optional, pre-created task ID
}

// LightRequest describes a lightweight tool execution.
type LightRequest struct {
	ParentSessionID string
	AgentID         string
	ToolName        string
	Input           map[string]any
}

// Result holds the outcome of a worker execution.
type Result struct {
	WorkerID   string        `json:"worker_id"`
	SessionID  string        `json:"session_id,omitempty"`
	TaskID     string        `json:"task_id,omitempty"`
	Content    string        `json:"content"`
	TokensUsed int           `json:"tokens_used"`
	Success    bool          `json:"success"`
	Error      string        `json:"error,omitempty"`
	Duration   time.Duration `json:"duration"`
}
