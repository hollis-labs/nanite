package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/coordination"
	"github.com/hollis-labs/nanite/internal/lifecycle"
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
	chat       ChatDelegator
	coord      coordination.CoordStore
	tasks      task.Service
	worktrees  worktree.Manager
	workers    sync.Map      // workerID -> *Worker
	sem        chan struct{} // concurrency semaphore
	maxWorkers int

	// lifecycle owns all goroutines spawned by this Manager (heartbeats,
	// deferred worker-map cleanups). Shutdown cancels its context and waits
	// for every tracked goroutine to exit before returning.
	lifecycle *lifecycle.Manager
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
		lifecycle:  lifecycle.NewManager("worker.manager"),
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

	// Start heartbeat as a tracked goroutine on the manager's lifecycle.
	// Per-worker cleanup closes heartbeatStop; Shutdown cancels the manager
	// context. Either one causes the heartbeat to exit.
	heartbeatStop := make(chan struct{})
	m.lifecycle.Go("heartbeat."+workerID[:8], func(ctx context.Context) {
		m.heartbeatLoop(ctx, workerID, heartbeatStop)
	})

	start := time.Now()

	// Cleanup function — always release semaphore and stop heartbeat.
	cleanup := func() {
		close(heartbeatStop)
		<-m.sem // release semaphore
		if w.GetWorktreePath() != "" && m.worktrees != nil {
			if err := m.worktrees.Cleanup(workerID); err != nil {
				slog.Warn("worker: worktree cleanup failed", "worker_id", workerID[:8], "err", err)
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
		w.SetWorktreePath(wtPath)
	}

	// Transition to running.
	w.SetStatus(StatusRunning)
	m.writeWorkerStatus(w)

	slog.Info("worker: spawning full worker",
		"worker_id", workerID[:8], "agent", req.AgentID, "isolation", req.Isolation)

	// Delegate the task.
	delegResult, delegErr := m.chat.DelegateTask(workerCtx, chat.DelegationRequest{
		ParentSessionID: req.ParentSessionID,
		Title:           req.Title,
		Description:     req.Description,
		AgentID:         req.AgentID,
		Mode:            req.Mode,
		Model:           req.Model,
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
		w.SetSessionID(delegResult.WorkerSessionID)
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

	// Remove from active workers after a short retention period for status
	// queries. Tracked on the manager lifecycle so Shutdown waits for it —
	// and so the 30s sleep is cancellable via ctx, closing BLG-001's leak.
	m.lifecycle.Go("retention."+workerID[:8], func(ctx context.Context) {
		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
		m.workers.Delete(workerID)
	})

	slog.Info("worker: finished", "worker_id", workerID[:8], "status", w.GetStatus(), "duration", result.Duration.Round(time.Millisecond))
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

	slog.Info("worker: light execution", "worker_id", workerID[:8], "tool", req.ToolName)

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

// List returns a snapshot of all active workers. Each returned Snapshot
// is a value-copy captured atomically under the worker mutex and is safe
// to read, share across goroutines, and JSON-marshal without further
// synchronization.
func (m *Manager) List() []Snapshot {
	var workers []Snapshot
	m.workers.Range(func(key, value any) bool {
		if w, ok := value.(*Worker); ok {
			workers = append(workers, w.Snapshot())
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
// longer than threshold). Returns snapshots of the reaped workers.
func (m *Manager) ReapStale(threshold time.Duration) []Snapshot {
	if m.coord == nil || !m.coord.Available() {
		return nil
	}

	var stale []Snapshot
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
			stale = append(stale, w.Snapshot())
			return true
		}

		var hb struct {
			Timestamp time.Time `json:"timestamp"`
		}
		if err := json.Unmarshal(data, &hb); err == nil {
			if time.Since(hb.Timestamp) > threshold {
				stale = append(stale, w.Snapshot())
			}
		}
		return true
	})

	for _, s := range stale {
		slog.Warn("worker: reaped as stale (heartbeat expired)", "worker_id", s.ID[:8])
		m.Cancel(s.ID)
	}

	return stale
}

// Shutdown cancels all active workers and waits up to maxWait for tracked
// goroutines (heartbeats, retention timers) to exit. Returns
// context.DeadlineExceeded if any goroutine is still running after maxWait.
//
// Before this refactor, Shutdown cancelled per-worker contexts but did not
// wait, and the 30-second retention goroutine in SpawnFull leaked past
// process exit (BLG-001). Both are now tracked by the manager's lifecycle.
func (m *Manager) Shutdown(maxWait time.Duration) error {
	m.workers.Range(func(key, value any) bool {
		if w, ok := value.(*Worker); ok {
			if w.cancel != nil {
				w.cancel()
			}
			w.SetStatus(StatusCancelled)
		}
		return true
	})
	return m.lifecycle.Shutdown(maxWait)
}

// writeWorkerStatus persists worker status to the coordination store.
// It captures a consistent snapshot of the worker fields under the worker
// mutex before marshaling so no concurrent SpawnFull/Cancel writer races
// against the read.
func (m *Manager) writeWorkerStatus(w *Worker) {
	if m.coord == nil || !m.coord.Available() {
		return
	}
	snap := w.Snapshot()
	data, _ := json.Marshal(map[string]any{
		"id":         snap.ID,
		"type":       snap.Type,
		"status":     snap.Status,
		"agent_id":   snap.AgentID,
		"session_id": snap.SessionID,
		"parent":     snap.ParentSessionID,
		"worktree":   snap.WorktreePath,
		"created_at": snap.CreatedAt,
	})
	_ = m.coord.Put(coordination.PrefixWorker+snap.ID+":status", data, coordination.WorkerTTL)
}

// heartbeatLoop writes periodic heartbeats to the coordination store.
// Exits when either the per-worker stop channel is closed (normal worker
// completion) or when ctx is cancelled (manager shutdown).
func (m *Manager) heartbeatLoop(ctx context.Context, workerID string, stop chan struct{}) {
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
		case <-ctx.Done():
			return
		case <-ticker.C:
			write()
		}
	}
}
