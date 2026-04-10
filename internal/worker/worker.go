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
// Worker is safe for concurrent access for the subset of fields that are
// mutated after the worker is published to Manager.workers:
//
//   - Status — transitioned by SpawnFull, Cancel, Shutdown, ReapStale
//   - SessionID — populated by SpawnFull after DelegateTask returns
//   - WorktreePath — populated by SpawnFull after worktree creation
//
// All in-package reads and writes of these fields after publication MUST
// go through the accessor methods below, which guard the fields with an
// RWMutex. The remaining fields (ID, Type, ParentSessionID, TaskID,
// AgentID, CreatedAt) are set once during construction before the worker
// is published and are treated as immutable thereafter.
//
// Callers outside this package should never dereference a *Worker
// directly; they should obtain a value-copy via Manager.List() or the
// Snapshot method, both of which atomically capture a consistent view of
// the mutable fields.
type Worker struct {
	ID              string             `json:"id"`
	Type            Type               `json:"type"`
	ParentSessionID string             `json:"parent_session_id"`
	SessionID       string             `json:"session_id,omitempty"`  // access via Get/SetSessionID
	TaskID          string             `json:"task_id,omitempty"`     // linked task
	AgentID         string             `json:"agent_id"`
	Status          Status             `json:"status"`                     // access via Get/SetStatus
	WorktreePath    string             `json:"worktree_path,omitempty"`    // access via Get/SetWorktreePath
	CreatedAt       time.Time          `json:"created_at"`
	cancel          context.CancelFunc `json:"-"`
	mu              sync.RWMutex       `json:"-"`
}

// Snapshot is a value-copy view of a Worker's exported fields captured
// atomically under the worker mutex. It is safe to read, share across
// goroutines, and JSON-marshal without further synchronization.
type Snapshot struct {
	ID              string    `json:"id"`
	Type            Type      `json:"type"`
	ParentSessionID string    `json:"parent_session_id"`
	SessionID       string    `json:"session_id,omitempty"`
	TaskID          string    `json:"task_id,omitempty"`
	AgentID         string    `json:"agent_id"`
	Status          Status    `json:"status"`
	WorktreePath    string    `json:"worktree_path,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
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

// GetSessionID returns the worker's current session ID under the worker mutex.
func (w *Worker) GetSessionID() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.SessionID
}

// SetSessionID updates the worker's session ID under the worker mutex.
func (w *Worker) SetSessionID(id string) {
	w.mu.Lock()
	w.SessionID = id
	w.mu.Unlock()
}

// GetWorktreePath returns the worker's current worktree path under the worker mutex.
func (w *Worker) GetWorktreePath() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.WorktreePath
}

// SetWorktreePath updates the worker's worktree path under the worker mutex.
func (w *Worker) SetWorktreePath(p string) {
	w.mu.Lock()
	w.WorktreePath = p
	w.mu.Unlock()
}

// Snapshot returns a value-copy view of the worker's fields captured
// atomically under the worker mutex.
func (w *Worker) Snapshot() Snapshot {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return Snapshot{
		ID:              w.ID,
		Type:            w.Type,
		ParentSessionID: w.ParentSessionID,
		SessionID:       w.SessionID,
		TaskID:          w.TaskID,
		AgentID:         w.AgentID,
		Status:          w.Status,
		WorktreePath:    w.WorktreePath,
		CreatedAt:       w.CreatedAt,
	}
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
