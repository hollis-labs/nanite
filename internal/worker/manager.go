package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/coordination"
	"github.com/hollis-labs/nanite/internal/task"
	"github.com/hollis-labs/nanite/internal/worktree"
)

// ChatDelegator is the subset of ChatService needed to delegate tasks.
type ChatDelegator interface {
	DelegateTask(ctx context.Context, req chat.DelegationRequest) (*chat.DelegationResult, error)
}

// ToolExecutor is the subset of ToolService needed for light workers.
type ToolExecutor interface {
	Execute(ctx context.Context, agentID, toolName string, input map[string]any) (content string, isError bool, err error)
}

// ManagerConfig holds dependencies for the worker manager.
type ManagerConfig struct {
	MaxConcurrentWorkers int // 0 = default (5)
	Chat                 ChatDelegator
	Coord                coordination.CoordStore
	Tasks                task.Service
	Worktrees            worktree.Manager
}

// Manager manages worker lifecycle with concurrency limiting.
type Manager struct {
	chat      ChatDelegator
	coord     coordination.CoordStore
	tasks     task.Service
	worktrees worktree.Manager
	workers   sync.Map     // workerID -> *Worker
	sem       chan struct{} // concurrency semaphore
	maxWorkers int
}

// NewManager creates a worker manager with the given configuration.
func NewManager(cfg ManagerConfig) *Manager {
	max := cfg.MaxConcurrentWorkers
	if max <= 0 {
		max = 5
	}
	return &Manager{
		chat:       cfg.Chat,
		coord:      cfg.Coord,
		tasks:      cfg.Tasks,
		worktrees:  cfg.Worktrees,
		sem:        make(chan struct{}, max),
		maxWorkers: max,
	}
}

// SpawnFull creates a full agent worker session with its own chat loop.
// It acquires a semaphore slot, optionally creates a worktree, delegates
// the task, and returns the result. The delegation blocks until completion.
func (m *Manager) SpawnFull(ctx context.Context, req SpawnRequest) (*Result, error) {
	// Acquire semaphore (non-blocking check + blocking wait).
	select {
	case m.sem <- struct{}{}:
		// acquired
	case <-ctx.Done():
		return nil, fmt.Errorf("worker spawn cancelled while waiting for capacity")
	}

	workerID := uuid.NewString()
	workerCtx, cancel := context.WithCancel(ctx)

	w := &Worker{
		ID:              workerID,
		Type:            TypeFull,
		ParentSessionID: req.ParentSessionID,
		AgentID:         req.AgentID,
		TaskID:          req.TaskID,
		Status:          StatusSpawning,
		CreatedAt:       time.Now().UTC(),
		cancel:          cancel,
	}
	m.workers.Store(workerID, w)

	// Write worker status to coordination store.
	m.writeWorkerStatus(w)

	// Start heartbeat.
	heartbeatStop := make(chan struct{})
	go m.heartbeatLoop(workerID, heartbeatStop)

	start := time.Now()

	// Cleanup function — always release semaphore and stop heartbeat.
	cleanup := func() {
		close(heartbeatStop)
		<-m.sem // release semaphore
		if w.WorktreePath != "" && m.worktrees != nil {
			if err := m.worktrees.Cleanup(workerID); err != nil {
				log.Printf("worker %s: worktree cleanup: %v", workerID[:8], err)
			}
		}
	}

	// Create worktree if isolation requested.
	if req.Isolation == "worktree" && m.worktrees != nil {
		wtPath, err := m.worktrees.Create(workerID)
		if err != nil {
			cleanup()
			m.workers.Delete(workerID)
			cancel()
			return nil, fmt.Errorf("create worktree: %w", err)
		}
		w.WorktreePath = wtPath
	}

	// Transition to running.
	w.SetStatus(StatusRunning)
	m.writeWorkerStatus(w)

	log.Printf("worker %s: spawning full worker (agent=%s, isolation=%s)",
		workerID[:8], req.AgentID, req.Isolation)

	// Delegate the task.
	delegResult, delegErr := m.chat.DelegateTask(workerCtx, chat.DelegationRequest{
		ParentSessionID: req.ParentSessionID,
		Title:           req.Title,
		Description:     req.Description,
		AgentID:         req.AgentID,
		Mode:            req.Mode,
		Model:           req.Model,
		WorkspaceID:     req.WorkspaceID,
	})

	// Build result.
	result := &Result{
		WorkerID: workerID,
		Duration: time.Since(start),
	}

	// A concurrent Shutdown/Cancel may have already marked this worker as
	// cancelled. Respect that terminal state rather than overwriting it.
	if delegErr != nil {
		if w.GetStatus() != StatusCancelled {
			w.SetStatus(StatusFailed)
		}
		result.Success = false
		result.Error = delegErr.Error()
	} else {
		w.SessionID = delegResult.WorkerSessionID
		result.SessionID = delegResult.WorkerSessionID
		result.TaskID = delegResult.TaskID
		result.Content = delegResult.Content
		result.TokensUsed = delegResult.TokensUsed
		result.Success = delegResult.Success
		if !delegResult.Success {
			if w.GetStatus() != StatusCancelled {
				w.SetStatus(StatusFailed)
			}
			result.Error = delegResult.Error
		} else {
			if w.GetStatus() != StatusCancelled {
				w.SetStatus(StatusCompleted)
			}
		}
	}

	m.writeWorkerStatus(w)
	cleanup()
	cancel()

	// Remove from active workers after a short retention period for status queries.
	go func() {
		time.Sleep(30 * time.Second)
		m.workers.Delete(workerID)
	}()

	log.Printf("worker %s: %s in %s", workerID[:8], w.GetStatus(), result.Duration.Round(time.Millisecond))
	return result, nil
}

// SpawnLight executes a single tool call without an LLM loop.
func (m *Manager) SpawnLight(ctx context.Context, executor ToolExecutor, req LightRequest) (*Result, error) {
	// Acquire semaphore.
	select {
	case m.sem <- struct{}{}:
		defer func() { <-m.sem }()
	case <-ctx.Done():
		return nil, fmt.Errorf("light worker cancelled while waiting for capacity")
	}

	workerID := uuid.NewString()
	start := time.Now()

	log.Printf("worker %s: light execution (tool=%s)", workerID[:8], req.ToolName)

	content, isError, err := executor.Execute(ctx, req.AgentID, req.ToolName, req.Input)

	result := &Result{
		WorkerID: workerID,
		Duration: time.Since(start),
		Content:  content,
		Success:  err == nil && !isError,
	}
	if err != nil {
		result.Error = err.Error()
	} else if isError {
		result.Error = content
	}

	return result, nil
}

// List returns all active workers.
func (m *Manager) List() []*Worker {
	var workers []*Worker
	m.workers.Range(func(key, value any) bool {
		if w, ok := value.(*Worker); ok {
			workers = append(workers, w)
		}
		return true
	})
	return workers
}

// Cancel cancels a specific worker by ID.
func (m *Manager) Cancel(workerID string) error {
	val, ok := m.workers.Load(workerID)
	if !ok {
		return fmt.Errorf("worker %s not found", workerID)
	}
	w := val.(*Worker)
	if w.cancel != nil {
		w.cancel()
	}
	w.SetStatus(StatusCancelled)
	m.writeWorkerStatus(w)

	// Cancel linked task if tracked.
	if w.TaskID != "" && m.tasks != nil {
		_ = m.tasks.Cancel(context.Background(), w.TaskID)
	}

	return nil
}

// ActiveCount returns the number of currently active workers.
func (m *Manager) ActiveCount() int {
	count := 0
	m.workers.Range(func(_, value any) bool {
		if w, ok := value.(*Worker); ok {
			s := w.GetStatus()
			if s == StatusSpawning || s == StatusRunning {
				count++
			}
		}
		return true
	})
	return count
}

// ReapStale cancels workers whose heartbeat has expired (no update for
// longer than threshold). Returns the list of reaped workers.
func (m *Manager) ReapStale(threshold time.Duration) []*Worker {
	if m.coord == nil || !m.coord.Available() {
		return nil
	}

	var stale []*Worker
	m.workers.Range(func(key, value any) bool {
		w, ok := value.(*Worker)
		if !ok || w.GetStatus() != StatusRunning {
			return true
		}

		// Check heartbeat in coordination store.
		hbKey := coordination.PrefixHeartbeat + w.ID + ":heartbeat"
		data, err := m.coord.Get(hbKey)
		if err != nil {
			// No heartbeat — stale.
			stale = append(stale, w)
			return true
		}

		var hb struct {
			Timestamp time.Time `json:"timestamp"`
		}
		if err := json.Unmarshal(data, &hb); err == nil {
			if time.Since(hb.Timestamp) > threshold {
				stale = append(stale, w)
			}
		}
		return true
	})

	for _, w := range stale {
		log.Printf("worker %s: reaped as stale (heartbeat expired)", w.ID[:8])
		m.Cancel(w.ID)
	}

	return stale
}

// Shutdown cancels all active workers.
func (m *Manager) Shutdown() {
	m.workers.Range(func(key, value any) bool {
		if w, ok := value.(*Worker); ok {
			if w.cancel != nil {
				w.cancel()
			}
			w.SetStatus(StatusCancelled)
		}
		return true
	})
}

// writeWorkerStatus persists worker status to the coordination store.
func (m *Manager) writeWorkerStatus(w *Worker) {
	if m.coord == nil || !m.coord.Available() {
		return
	}
	data, _ := json.Marshal(map[string]any{
		"id":         w.ID,
		"type":       w.Type,
		"status":     w.GetStatus(),
		"agent_id":   w.AgentID,
		"session_id": w.SessionID,
		"parent":     w.ParentSessionID,
		"worktree":   w.WorktreePath,
		"created_at": w.CreatedAt,
	})
	_ = m.coord.Put(coordination.PrefixWorker+w.ID+":status", data, coordination.WorkerTTL)
}

// heartbeatLoop writes periodic heartbeats to the coordination store.
func (m *Manager) heartbeatLoop(workerID string, stop chan struct{}) {
	if m.coord == nil || !m.coord.Available() {
		return
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	write := func() {
		data, _ := json.Marshal(map[string]any{
			"worker_id": workerID,
			"timestamp": time.Now().UTC(),
		})
		_ = m.coord.Put(
			coordination.PrefixHeartbeat+workerID+":heartbeat",
			data,
			coordination.HeartbeatTTL,
		)
	}

	write() // initial heartbeat
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			write()
		}
	}
}
