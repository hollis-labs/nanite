package task

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/coordination"
)

// LocalBackend implements TaskBackend using the coordination store (Badger KV)
// as the primary store, with SQLite snapshots for persistence across restarts.
type LocalBackend struct {
	coord coordination.CoordStore
	db    SnapshotStore
}

// NewLocalBackend creates a local task backend.
func NewLocalBackend(coord coordination.CoordStore, db SnapshotStore) *LocalBackend {
	return &LocalBackend{coord: coord, db: db}
}

func (b *LocalBackend) Create(ctx context.Context, t *Task) error {
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
	return b.put(t)
}

func (b *LocalBackend) Get(ctx context.Context, id string) (*Task, error) {
	data, err := b.coord.Get(coordination.PrefixTask + id)
	if err != nil {
		return nil, fmt.Errorf("get task %s: %w", id, err)
	}
	var t Task
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("unmarshal task %s: %w", id, err)
	}
	return &t, nil
}

func (b *LocalBackend) Update(ctx context.Context, t *Task) error {
	t.UpdatedAt = time.Now().UTC()
	return b.put(t)
}

func (b *LocalBackend) Delete(ctx context.Context, id string) error {
	return b.coord.Delete(coordination.PrefixTask + id)
}

func (b *LocalBackend) List(ctx context.Context, filter TaskFilter) ([]*Task, error) {
	entries, err := b.coord.List(coordination.PrefixTask)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	var tasks []*Task
	for _, e := range entries {
		var t Task
		if err := json.Unmarshal(e.Value, &t); err != nil {
			continue
		}
		if !matchesFilter(&t, filter) {
			continue
		}
		tasks = append(tasks, &t)
	}
	return tasks, nil
}

func (b *LocalBackend) Transition(ctx context.Context, id string, to Status) error {
	t, err := b.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := ValidateTransition(t.Status, to); err != nil {
		return err
	}
	t.Status = to
	t.UpdatedAt = time.Now().UTC()
	if to == StatusCompleted || to == StatusFailed || to == StatusCanceled {
		now := time.Now().UTC()
		t.CompletedAt = &now
	}
	return b.put(t)
}

func (b *LocalBackend) Assign(ctx context.Context, id, agentID, workerSessionID string) error {
	t, err := b.Get(ctx, id)
	if err != nil {
		return err
	}
	t.AssigneeAgentID = agentID
	t.WorkerSessionID = workerSessionID
	t.UpdatedAt = time.Now().UTC()
	return b.put(t)
}

// Snapshot persists all in-flight tasks to SQLite.
func (b *LocalBackend) Snapshot(ctx context.Context) error {
	if b.db == nil {
		return nil
	}
	entries, err := b.coord.List(coordination.PrefixTask)
	if err != nil {
		return fmt.Errorf("list tasks for snapshot: %w", err)
	}
	for _, e := range entries {
		var t Task
		if err := json.Unmarshal(e.Value, &t); err != nil {
			continue
		}
		if err := b.db.UpsertTask(&t); err != nil {
			return fmt.Errorf("snapshot task %s: %w", t.ID, err)
		}
	}
	return nil
}

// Restore reloads non-terminal tasks from SQLite into the coord store.
func (b *LocalBackend) Restore(ctx context.Context) error {
	if b.db == nil || !b.coord.Available() {
		return nil
	}
	tasks, err := b.db.ListTasks(TaskFilter{})
	if err != nil {
		return fmt.Errorf("restore tasks: %w", err)
	}
	for _, t := range tasks {
		if t.IsTerminal() {
			continue
		}
		if err := b.put(t); err != nil {
			return fmt.Errorf("restore task %s: %w", t.ID, err)
		}
	}
	return nil
}

func (b *LocalBackend) put(t *Task) error {
	data, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("marshal task: %w", err)
	}
	return b.coord.Put(coordination.PrefixTask+t.ID, data, 0)
}

func matchesFilter(t *Task, f TaskFilter) bool {
	if f.SessionID != "" && t.SessionID != f.SessionID {
		return false
	}
	if f.ParentID != "" && t.ParentID != f.ParentID {
		return false
	}
	if f.Status != "" && t.Status != f.Status {
		return false
	}
	return true
}
