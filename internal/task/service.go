package task

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/coordination"
)

// Service manages task lifecycle for multi-agent orchestration.
type Service interface {
	Create(ctx context.Context, t *Task) error
	Get(ctx context.Context, id string) (*Task, error)
	Update(ctx context.Context, t *Task) error
	Transition(ctx context.Context, id string, to Status) error
	Assign(ctx context.Context, id, agentID, workerSessionID string) error
	ListBySession(ctx context.Context, sessionID string) ([]*Task, error)
	ListByParent(ctx context.Context, parentID string) ([]*Task, error)
	ListAll(ctx context.Context) ([]*Task, error)
	Cancel(ctx context.Context, id string) error
	Snapshot(ctx context.Context) error
	Restore(ctx context.Context) error
}

// SnapshotStore is the subset of the SQLite store needed for task snapshots.
type SnapshotStore interface {
	UpsertTask(t *Task) error
	ListTasks(filter TaskFilter) ([]*Task, error)
}

// TaskFilter controls which tasks to retrieve from the snapshot store.
type TaskFilter struct {
	SessionID string
	ParentID  string
	Status    Status
}

// ServiceConfig holds dependencies for the task service.
type ServiceConfig struct {
	Coord coordination.CoordStore
	DB    SnapshotStore
}

type serviceImpl struct {
	coord coordination.CoordStore
	db    SnapshotStore
}

// NewService creates a new task service backed by the coordination store
// with SQLite snapshots for persistence.
func NewService(cfg ServiceConfig) Service {
	return &serviceImpl{
		coord: cfg.Coord,
		db:    cfg.DB,
	}
}

func (s *serviceImpl) Create(ctx context.Context, t *Task) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	t.CreatedAt = now
	t.UpdatedAt = now
	if t.Status == "" {
		t.Status = StatusPending
	}
	if t.Metadata == nil {
		t.Metadata = make(map[string]string)
	}
	return s.put(t)
}

func (s *serviceImpl) Get(ctx context.Context, id string) (*Task, error) {
	data, err := s.coord.Get(coordination.PrefixTask + id)
	if err != nil {
		return nil, fmt.Errorf("get task %s: %w", id, err)
	}
	var t Task
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("unmarshal task %s: %w", id, err)
	}
	return &t, nil
}

func (s *serviceImpl) Update(ctx context.Context, t *Task) error {
	t.UpdatedAt = time.Now().UTC()
	return s.put(t)
}

func (s *serviceImpl) Transition(ctx context.Context, id string, to Status) error {
	t, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := ValidateTransition(t.Status, to); err != nil {
		return err
	}
	t.Status = to
	t.UpdatedAt = time.Now().UTC()
	if to == StatusCompleted || to == StatusFailed || to == StatusCancelled {
		now := time.Now().UTC()
		t.CompletedAt = &now
	}
	return s.put(t)
}

func (s *serviceImpl) Assign(ctx context.Context, id, agentID, workerSessionID string) error {
	t, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	t.AssigneeAgentID = agentID
	t.WorkerSessionID = workerSessionID
	t.UpdatedAt = time.Now().UTC()
	return s.put(t)
}

func (s *serviceImpl) ListBySession(ctx context.Context, sessionID string) ([]*Task, error) {
	return s.listFiltered(func(t *Task) bool { return t.SessionID == sessionID })
}

func (s *serviceImpl) ListByParent(ctx context.Context, parentID string) ([]*Task, error) {
	return s.listFiltered(func(t *Task) bool { return t.ParentID == parentID })
}

func (s *serviceImpl) ListAll(ctx context.Context) ([]*Task, error) {
	return s.listFiltered(func(t *Task) bool { return true })
}

func (s *serviceImpl) Cancel(ctx context.Context, id string) error {
	return s.Transition(ctx, id, StatusCancelled)
}

func (s *serviceImpl) Snapshot(ctx context.Context) error {
	if s.db == nil {
		return nil
	}
	entries, err := s.coord.List(coordination.PrefixTask)
	if err != nil {
		return fmt.Errorf("list tasks for snapshot: %w", err)
	}
	for _, e := range entries {
		var t Task
		if err := json.Unmarshal(e.Value, &t); err != nil {
			continue
		}
		if err := s.db.UpsertTask(&t); err != nil {
			return fmt.Errorf("snapshot task %s: %w", t.ID, err)
		}
	}
	return nil
}

func (s *serviceImpl) Restore(ctx context.Context) error {
	if s.db == nil || !s.coord.Available() {
		return nil
	}
	tasks, err := s.db.ListTasks(TaskFilter{})
	if err != nil {
		return fmt.Errorf("restore tasks: %w", err)
	}
	for _, t := range tasks {
		if t.IsTerminal() {
			continue
		}
		if err := s.put(t); err != nil {
			return fmt.Errorf("restore task %s: %w", t.ID, err)
		}
	}
	return nil
}

func (s *serviceImpl) put(t *Task) error {
	data, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("marshal task: %w", err)
	}
	return s.coord.Put(coordination.PrefixTask+t.ID, data, 0)
}

func (s *serviceImpl) listFiltered(pred func(*Task) bool) ([]*Task, error) {
	entries, err := s.coord.List(coordination.PrefixTask)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	var tasks []*Task
	for _, e := range entries {
		var t Task
		if err := json.Unmarshal(e.Value, &t); err != nil {
			continue
		}
		if pred(&t) {
			tasks = append(tasks, &t)
		}
	}
	return tasks, nil
}
