package task

import (
	"context"
	"database/sql"
	"testing"

	"github.com/hollis-labs/nanite/internal/coordination"
	_ "modernc.org/sqlite"
)

func setupTestService(t *testing.T) (Service, *sql.DB) {
	t.Helper()

	coord, err := coordination.NewBadgerStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBadgerStore: %v", err)
	}
	t.Cleanup(func() { coord.Close() })

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Create tasks table.
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS tasks (
		id TEXT PRIMARY KEY,
		parent_id TEXT,
		session_id TEXT NOT NULL,
		worker_session_id TEXT,
		title TEXT NOT NULL,
		description TEXT,
		status TEXT NOT NULL DEFAULT 'pending',
		assignee_agent_id TEXT,
		result TEXT,
		error TEXT,
		tokens_used INTEGER DEFAULT 0,
		metadata TEXT DEFAULT '{}',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		completed_at TEXT
	)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	local := NewLocalBackend(coord, &SQLiteSnapshot{DB: db})
	svc := NewService(ServiceConfig{
		Local: local,
	})
	return svc, db
}

func TestCreateAndGet(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	task := &Task{
		SessionID:   "sess-1",
		Title:       "Test task",
		Description: "Do something",
	}
	if err := svc.Create(ctx, task); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if task.ID == "" {
		t.Fatal("task ID should be set")
	}
	if task.Status != StatusPending {
		t.Errorf("status = %q, want pending", task.Status)
	}

	got, err := svc.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "Test task" {
		t.Errorf("title = %q, want %q", got.Title, "Test task")
	}
}

func TestTransitionValid(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	task := &Task{SessionID: "s1", Title: "t1"}
	_ = svc.Create(ctx, task)

	// pending -> in_progress
	if err := svc.Transition(ctx, task.ID, StatusInProgress); err != nil {
		t.Fatalf("pending->in_progress: %v", err)
	}

	got, _ := svc.Get(ctx, task.ID)
	if got.Status != StatusInProgress {
		t.Errorf("status = %q, want in_progress", got.Status)
	}

	// in_progress -> completed
	if err := svc.Transition(ctx, task.ID, StatusCompleted); err != nil {
		t.Fatalf("in_progress->completed: %v", err)
	}

	got, _ = svc.Get(ctx, task.ID)
	if got.Status != StatusCompleted {
		t.Errorf("status = %q, want completed", got.Status)
	}
	if got.CompletedAt == nil {
		t.Error("CompletedAt should be set")
	}
}

func TestTransitionInvalid(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	task := &Task{SessionID: "s1", Title: "t1"}
	_ = svc.Create(ctx, task)

	// pending -> completed (invalid — must go through in_progress)
	err := svc.Transition(ctx, task.ID, StatusCompleted)
	if err == nil {
		t.Fatal("expected error for invalid transition")
	}
}

func TestAssign(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	task := &Task{SessionID: "s1", Title: "t1"}
	_ = svc.Create(ctx, task)

	if err := svc.Assign(ctx, task.ID, "agent-1", "worker-sess-1"); err != nil {
		t.Fatalf("Assign: %v", err)
	}

	got, _ := svc.Get(ctx, task.ID)
	if got.AssigneeAgentID != "agent-1" {
		t.Errorf("assignee = %q, want agent-1", got.AssigneeAgentID)
	}
	if got.WorkerSessionID != "worker-sess-1" {
		t.Errorf("worker session = %q, want worker-sess-1", got.WorkerSessionID)
	}
}

func TestListBySession(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	_ = svc.Create(ctx, &Task{SessionID: "s1", Title: "t1"})
	_ = svc.Create(ctx, &Task{SessionID: "s1", Title: "t2"})
	_ = svc.Create(ctx, &Task{SessionID: "s2", Title: "t3"})

	tasks, err := svc.ListBySession(ctx, "s1")
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("got %d tasks, want 2", len(tasks))
	}
}

func TestListByParent(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	parent := &Task{SessionID: "s1", Title: "parent"}
	_ = svc.Create(ctx, parent)

	_ = svc.Create(ctx, &Task{SessionID: "s1", ParentID: parent.ID, Title: "child1"})
	_ = svc.Create(ctx, &Task{SessionID: "s1", ParentID: parent.ID, Title: "child2"})
	_ = svc.Create(ctx, &Task{SessionID: "s1", Title: "orphan"})

	children, err := svc.ListByParent(ctx, parent.ID)
	if err != nil {
		t.Fatalf("ListByParent: %v", err)
	}
	if len(children) != 2 {
		t.Errorf("got %d children, want 2", len(children))
	}
}

func TestCancel(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	task := &Task{SessionID: "s1", Title: "t1"}
	_ = svc.Create(ctx, task)

	if err := svc.Cancel(ctx, task.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	got, _ := svc.Get(ctx, task.ID)
	if got.Status != StatusCancelled {
		t.Errorf("status = %q, want cancelled", got.Status)
	}
}

func TestSnapshotAndRestore(t *testing.T) {
	svc, db := setupTestService(t)
	ctx := context.Background()

	// Create tasks in various states.
	active := &Task{SessionID: "s1", Title: "active"}
	_ = svc.Create(ctx, active)
	_ = svc.Transition(ctx, active.ID, StatusInProgress)

	done := &Task{SessionID: "s1", Title: "done"}
	_ = svc.Create(ctx, done)
	_ = svc.Transition(ctx, done.ID, StatusInProgress)
	_ = svc.Transition(ctx, done.ID, StatusCompleted)

	// Snapshot to SQLite.
	if err := svc.Snapshot(ctx); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// Verify rows in SQLite.
	var count int
	db.QueryRow("SELECT COUNT(*) FROM tasks").Scan(&count)
	if count != 2 {
		t.Errorf("snapshot: %d rows, want 2", count)
	}

	// Create a new service (simulating restart) and restore.
	coord2, _ := coordination.NewBadgerStore(t.TempDir())
	t.Cleanup(func() { coord2.Close() })

	local2 := NewLocalBackend(coord2, &SQLiteSnapshot{DB: db})
	svc2 := NewService(ServiceConfig{
		Local: local2,
	})

	if err := svc2.Restore(ctx); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// Only non-terminal tasks should be restored.
	all, _ := svc2.ListAll(ctx)
	if len(all) != 1 {
		t.Errorf("restored %d tasks, want 1 (only in_progress)", len(all))
	}
	if len(all) > 0 && all[0].Title != "active" {
		t.Errorf("restored task = %q, want %q", all[0].Title, "active")
	}
}

func TestValidateTransition(t *testing.T) {
	tests := []struct {
		from, to Status
		wantErr  bool
	}{
		{StatusPending, StatusInProgress, false},
		{StatusPending, StatusCancelled, false},
		{StatusPending, StatusCompleted, true},
		{StatusPending, StatusFailed, true},
		{StatusInProgress, StatusCompleted, false},
		{StatusInProgress, StatusFailed, false},
		{StatusInProgress, StatusCancelled, false},
		{StatusInProgress, StatusPending, true},
		{StatusCompleted, StatusPending, true},
		{StatusCompleted, StatusInProgress, true},
		{StatusFailed, StatusPending, false}, // retry
		{StatusFailed, StatusCompleted, true},
		{StatusCancelled, StatusPending, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.from)+"->"+string(tt.to), func(t *testing.T) {
			err := ValidateTransition(tt.from, tt.to)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateTransition(%q, %q) = %v, wantErr %v", tt.from, tt.to, err, tt.wantErr)
			}
		})
	}
}
