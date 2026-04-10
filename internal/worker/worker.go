package worker

import (
	"context"
	"sync"
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
//
// Worker is safe for concurrent access: the mutable Status field must be
// read and written through GetStatus and SetStatus, which guard it with
// an RWMutex. Other fields are only written during SpawnFull before the
// worker becomes visible to other goroutines and are treated as immutable
// thereafter, with the exception of SessionID and WorktreePath which are
// set only from within SpawnFull before the final status transition.
type Worker struct {
	ID              string             `json:"id"`
	Type            Type               `json:"type"`
	ParentSessionID string             `json:"parent_session_id"`
	SessionID       string             `json:"session_id,omitempty"`   // for full workers
	TaskID          string             `json:"task_id,omitempty"`      // linked task
	AgentID         string             `json:"agent_id"`
	Status          Status             `json:"status"` // access via GetStatus/SetStatus
	WorktreePath    string             `json:"worktree_path,omitempty"`
	CreatedAt       time.Time          `json:"created_at"`
	cancel          context.CancelFunc `json:"-"`
	mu              sync.RWMutex       `json:"-"`
}

// GetStatus returns the worker's current status, guarded by the worker mutex.
func (w *Worker) GetStatus() Status {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.Status
}

// SetStatus updates the worker's current status, guarded by the worker mutex.
func (w *Worker) SetStatus(s Status) {
	w.mu.Lock()
	w.Status = s
	w.mu.Unlock()
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
