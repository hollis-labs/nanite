package service

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/coordination"
	"github.com/hollis-labs/nanite/internal/task"
)

// slowListCoord delays the snapshot's read of the coordination store, so an
// ordering bug (store closed before the snapshot reads it) shows on every run.
type slowListCoord struct {
	coordination.CoordStore
	delay time.Duration
}

func (c slowListCoord) List(prefix string) ([]coordination.KVEntry, error) {
	time.Sleep(c.delay)
	return c.CoordStore.List(prefix)
}

type nopSnapshotStore struct{}

func (nopSnapshotStore) UpsertTask(*task.Task) error { return nil }
func (nopSnapshotStore) ListTasks(task.TaskFilter) ([]*task.Task, error) {
	return nil, nil
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// CW-20261011-0082: the tasks snapshot reads the coordination store, so the
// store must not be closed until the snapshot has finished. A clean shutdown
// must log no subsystem panic.
func TestContainer_ShutdownSnapshotsTasksBeforeClosingCoord(t *testing.T) {
	badger, err := coordination.NewBadgerStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBadgerStore: %v", err)
	}
	coord := slowListCoord{CoordStore: badger, delay: 50 * time.Millisecond}
	local := task.NewLocalBackend(coord, nopSnapshotStore{})
	tasks := task.NewService(task.ServiceConfig{Local: local})
	for i := 0; i < 3; i++ {
		if err := local.Create(context.Background(), &task.Task{Title: "t"}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	var logs lockedBuffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	c := &Container{Coord: coord, Tasks: tasks}
	c.shutdownWithMaxWait(5 * time.Second)

	out := logs.String()
	if strings.Contains(out, "subsystem panic") || strings.Contains(out, "task snapshot") {
		t.Fatalf("shutdown logged an error:\n%s", out)
	}
}
