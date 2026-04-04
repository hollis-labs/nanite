package task

import "context"

// TaskBackend is the pluggable storage abstraction for task management.
// Implementations provide CRUD + lifecycle operations. The local SQLite/coord
// backend is always available; plugins can register alternatives (Engine, Linear, etc.).
type TaskBackend interface {
	Create(ctx context.Context, t *Task) error
	Get(ctx context.Context, id string) (*Task, error)
	Update(ctx context.Context, t *Task) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, filter TaskFilter) ([]*Task, error)
	Transition(ctx context.Context, id string, to Status) error
	Assign(ctx context.Context, id, agentID, workerSessionID string) error
}

// BackendName is the well-known name for the built-in local backend.
const BackendLocal = "local"
