package task

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
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

	// RegisterBackend adds a named TaskBackend. The "local" backend is always
	// registered at construction time. The backend must implement TaskBackend.
	// Accepts interface{} so the plugin host can call it without importing this package.
	RegisterBackend(name string, backend interface{})

	// UnregisterBackend removes a named backend and returns true if it was
	// present. Used by the plugin host on UnloadPlugin to sweep backends
	// registered by a now-unloaded plugin. The "local" backend is protected
	// (callers must not unregister it); the host only invokes this for names
	// it tracked in taskBackendOwners during RegisterTaskBackend.
	UnregisterBackend(name string) bool
}

// SnapshotStore is the subset of the SQLite store needed for task snapshots.
type SnapshotStore interface {
	UpsertTask(t *Task) error
	ListTasks(filter TaskFilter) ([]*Task, error)
}

// TaskFilter controls which tasks to retrieve.
type TaskFilter struct {
	SessionID string
	ParentID  string
	Status    Status
}

// SettingsFunc returns the user's configured task_backend name.
// If it returns "" or an error, the service falls back to "local".
type SettingsFunc func() string

// ServiceConfig holds dependencies for the task service.
type ServiceConfig struct {
	Local    *LocalBackend
	Settings SettingsFunc
}

type serviceImpl struct {
	mu       sync.RWMutex
	backends map[string]TaskBackend
	local    *LocalBackend
	settings SettingsFunc
}

// NewService creates a task service with the local backend pre-registered.
func NewService(cfg ServiceConfig) Service {
	s := &serviceImpl{
		backends: make(map[string]TaskBackend),
		local:    cfg.Local,
		settings: cfg.Settings,
	}
	if s.settings == nil {
		s.settings = func() string { return BackendLocal }
	}
	if cfg.Local != nil {
		s.backends[BackendLocal] = cfg.Local
	}
	return s
}

func (s *serviceImpl) RegisterBackend(name string, backend interface{}) {
	tb, ok := backend.(TaskBackend)
	if !ok {
		slog.Error("task backend registration failed: does not implement TaskBackend", "name", name)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backends[name] = tb
	slog.Info("task backend registered", "name", name)
}

// UnregisterBackend removes a named backend. The built-in "local" backend is
// protected — attempts to unregister it are rejected with a warning so the
// service always retains its fallback even if a plugin misbehaves. Returns
// true if the backend was present and removed, false otherwise.
func (s *serviceImpl) UnregisterBackend(name string) bool {
	if name == BackendLocal {
		slog.Warn("task backend unregister rejected: local backend is protected", "name", name)
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.backends[name]; !ok {
		return false
	}
	delete(s.backends, name)
	slog.Info("task backend unregistered", "name", name)
	return true
}

// active resolves the current backend. Falls back to local with a warning
// if the configured backend is unavailable.
func (s *serviceImpl) active() TaskBackend {
	name := s.settings()
	if name == "" {
		name = BackendLocal
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if b, ok := s.backends[name]; ok {
		return b
	}
	if name != BackendLocal {
		slog.Warn("configured task backend unavailable, falling back to local", "backend", name)
	}
	return s.local
}

func (s *serviceImpl) Create(ctx context.Context, t *Task) error {
	return s.active().Create(ctx, t)
}

func (s *serviceImpl) Get(ctx context.Context, id string) (*Task, error) {
	return s.active().Get(ctx, id)
}

func (s *serviceImpl) Update(ctx context.Context, t *Task) error {
	return s.active().Update(ctx, t)
}

func (s *serviceImpl) Transition(ctx context.Context, id string, to Status) error {
	return s.active().Transition(ctx, id, to)
}

func (s *serviceImpl) Assign(ctx context.Context, id, agentID, workerSessionID string) error {
	return s.active().Assign(ctx, id, agentID, workerSessionID)
}

func (s *serviceImpl) ListBySession(ctx context.Context, sessionID string) ([]*Task, error) {
	return s.active().List(ctx, TaskFilter{SessionID: sessionID})
}

func (s *serviceImpl) ListByParent(ctx context.Context, parentID string) ([]*Task, error) {
	return s.active().List(ctx, TaskFilter{ParentID: parentID})
}

func (s *serviceImpl) ListAll(ctx context.Context) ([]*Task, error) {
	return s.active().List(ctx, TaskFilter{})
}

func (s *serviceImpl) Cancel(ctx context.Context, id string) error {
	return s.Transition(ctx, id, StatusCancelled)
}

// Snapshot delegates to the local backend only (other backends manage their own persistence).
func (s *serviceImpl) Snapshot(ctx context.Context) error {
	if s.local == nil {
		return nil
	}
	return s.local.Snapshot(ctx)
}

// Restore delegates to the local backend only.
func (s *serviceImpl) Restore(ctx context.Context) error {
	if s.local == nil {
		return nil
	}
	return s.local.Restore(ctx)
}

// Delete removes a task by ID from the active backend.
func (s *serviceImpl) Delete(ctx context.Context, id string) error {
	return s.active().Delete(ctx, id)
}

// ActiveBackendName returns the name of the currently active backend (for diagnostics).
func ActiveBackendName(svc Service) string {
	if si, ok := svc.(*serviceImpl); ok {
		name := si.settings()
		if name == "" {
			return BackendLocal
		}
		si.mu.RLock()
		_, found := si.backends[name]
		si.mu.RUnlock()
		if found {
			return name
		}
		return fmt.Sprintf("%s (unavailable, using local)", name)
	}
	return "unknown"
}
