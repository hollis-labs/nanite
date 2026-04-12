package worker

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/coordination"
)

// stubDelegator is a mock ChatDelegator for testing.
type stubDelegator struct {
	delay   time.Duration
	fail    bool
	content string
	calls   atomic.Int32
}

func (s *stubDelegator) DelegateTask(ctx context.Context, req chat.DelegationRequest) (*chat.DelegationResult, error) {
	s.calls.Add(1)
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if s.fail {
		return nil, fmt.Errorf("deliberate failure")
	}
	content := s.content
	if content == "" {
		content = "result for: " + req.Title
	}
	return &chat.DelegationResult{
		WorkerSessionID: "worker-sess-1",
		Content:         content,
		TokensUsed:      100,
		Success:         true,
	}, nil
}

// stubToolExecutor is a mock ToolExecutor for testing.
type stubToolExecutor struct{}

func (s *stubToolExecutor) Execute(ctx context.Context, agentID, toolName string, input map[string]any) (string, bool, error) {
	return "tool result: " + toolName, false, nil
}

func newTestManager(delegator ChatDelegator) *Manager {
	coord := coordination.NewNoopStore()
	m := NewManager(ManagerConfig{
		MaxConcurrentWorkers: 3,
		Chat:                 delegator,
		Coord:                coord,
	})
	testManagersMu.Lock()
	testManagers = append(testManagers, m)
	testManagersMu.Unlock()
	return m
}

// testManagers tracks all test-created managers so TestMain can drain them
// via Shutdown before goleak.VerifyTestMain runs its leak check. Before
// lifecycle-based tracking, the manager's 30-second retention goroutine
// would outlive the test and trip goleak.
var (
	testManagers   []*Manager
	testManagersMu sync.Mutex
)

func TestSpawnFull(t *testing.T) {
	deleg := &stubDelegator{content: "hello from worker"}
	mgr := newTestManager(deleg)

	result, err := mgr.SpawnFull(context.Background(), SpawnRequest{
		ParentSessionID: "parent-1",
		Title:           "Test task",
		Description:     "Do something",
		AgentID:         "agent-1",
	})
	if err != nil {
		t.Fatalf("SpawnFull: %v", err)
	}
	if !result.Success {
		t.Errorf("Success = false, error: %s", result.Error)
	}
	if result.Content != "hello from worker" {
		t.Errorf("Content = %q", result.Content)
	}
	if result.WorkerID == "" {
		t.Error("WorkerID should be set")
	}
	if result.Duration == 0 {
		t.Error("Duration should be > 0")
	}
}

func TestSpawnFullFailure(t *testing.T) {
	deleg := &stubDelegator{fail: true}
	mgr := newTestManager(deleg)

	result, err := mgr.SpawnFull(context.Background(), SpawnRequest{
		ParentSessionID: "parent-1",
		Title:           "Failing task",
		AgentID:         "agent-1",
	})
	if err != nil {
		t.Fatalf("SpawnFull: %v", err)
	}
	if result.Success {
		t.Error("expected failure")
	}
	if result.Error == "" {
		t.Error("Error should be set")
	}
}

func TestConcurrencyLimit(t *testing.T) {
	deleg := &stubDelegator{delay: 200 * time.Millisecond}
	mgr := NewManager(ManagerConfig{
		MaxConcurrentWorkers: 2,
		Chat:                 deleg,
		Coord:                coordination.NewNoopStore(),
	})
	testManagersMu.Lock()
	testManagers = append(testManagers, mgr)
	testManagersMu.Unlock()

	// Spawn 3 workers concurrently. With limit=2, the 3rd must wait.
	done := make(chan *Result, 3)
	for i := 0; i < 3; i++ {
		go func(i int) {
			r, _ := mgr.SpawnFull(context.Background(), SpawnRequest{
				ParentSessionID: "p-1",
				Title:           fmt.Sprintf("task-%d", i),
				AgentID:         "agent-1",
			})
			done <- r
		}(i)
	}

	// Collect all results.
	var results []*Result
	for i := 0; i < 3; i++ {
		results = append(results, <-done)
	}

	// All should succeed.
	for i, r := range results {
		if !r.Success {
			t.Errorf("worker %d failed: %s", i, r.Error)
		}
	}

	// Total delegation calls should be 3.
	if calls := deleg.calls.Load(); calls != 3 {
		t.Errorf("delegation calls = %d, want 3", calls)
	}
}

func TestCancelWorker(t *testing.T) {
	deleg := &stubDelegator{delay: 5 * time.Second}
	mgr := newTestManager(deleg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Spawn a long-running worker in background.
	done := make(chan *Result, 1)
	go func() {
		r, _ := mgr.SpawnFull(ctx, SpawnRequest{
			ParentSessionID: "p-1",
			Title:           "long task",
			AgentID:         "agent-1",
		})
		done <- r
	}()

	// Wait for worker to appear.
	time.Sleep(50 * time.Millisecond)

	workers := mgr.List()
	if len(workers) == 0 {
		t.Fatal("expected at least 1 worker")
	}

	// Cancel the worker.
	if err := mgr.Cancel(workers[0].ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	// Wait for result.
	result := <-done
	if result.Success {
		t.Error("expected cancelled worker to not succeed")
	}
}

func TestSpawnLight(t *testing.T) {
	mgr := newTestManager(nil)
	executor := &stubToolExecutor{}

	result, err := mgr.SpawnLight(context.Background(), executor, LightRequest{
		ParentSessionID: "p-1",
		AgentID:         "agent-1",
		ToolName:        "grep",
		Input:           map[string]any{"pattern": "foo"},
	})
	if err != nil {
		t.Fatalf("SpawnLight: %v", err)
	}
	if !result.Success {
		t.Errorf("Success = false, error: %s", result.Error)
	}
	if result.Content != "tool result: grep" {
		t.Errorf("Content = %q", result.Content)
	}
}

func TestListAndActiveCount(t *testing.T) {
	deleg := &stubDelegator{delay: 200 * time.Millisecond}
	mgr := newTestManager(deleg)

	done := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			mgr.SpawnFull(context.Background(), SpawnRequest{
				ParentSessionID: "p-1",
				Title:           fmt.Sprintf("task-%d", i),
				AgentID:         "agent-1",
			})
			done <- struct{}{}
		}(i)
	}

	// Wait briefly for workers to start.
	time.Sleep(50 * time.Millisecond)

	workers := mgr.List()
	if len(workers) < 1 {
		t.Errorf("List: got %d workers, want >= 1", len(workers))
	}

	active := mgr.ActiveCount()
	if active < 1 {
		t.Errorf("ActiveCount = %d, want >= 1", active)
	}

	// Wait for completion.
	for i := 0; i < 2; i++ {
		<-done
	}
}

func TestShutdown(t *testing.T) {
	deleg := &stubDelegator{delay: 5 * time.Second}
	mgr := newTestManager(deleg)

	go mgr.SpawnFull(context.Background(), SpawnRequest{
		ParentSessionID: "p-1",
		Title:           "long task",
		AgentID:         "agent-1",
	})

	time.Sleep(50 * time.Millisecond)
	_ = mgr.Shutdown(2 * time.Second)

	// All workers should be cancelled. List returns value snapshots, so
	// field reads here are safe without further synchronization.
	workers := mgr.List()
	for _, w := range workers {
		if w.Status != StatusCancelled {
			t.Errorf("worker %s status = %q, want cancelled", w.ID[:8], w.Status)
		}
	}
}

// TestListConcurrentFieldAccess stresses concurrent Manager.List() reads
// against an in-flight SpawnFull that writes SessionID and WorktreePath
// after the worker has been published to the sync.Map. Run with -race;
// without synchronization on those fields the race detector flags the
// reader/writer pair.
func TestListConcurrentFieldAccess(t *testing.T) {
	// Small delay gives readers time to observe the worker before
	// DelegateTask returns and the SessionID write happens.
	deleg := &stubDelegator{delay: 20 * time.Millisecond, content: "hi"}
	mgr := newTestManager(deleg)

	const spawners = 4
	const readersPerSpawner = 4
	const iterations = 50

	stop := make(chan struct{})
	var readerWG sync.WaitGroup
	for i := 0; i < spawners*readersPerSpawner; i++ {
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, w := range mgr.List() {
					// Touch every concurrently-mutable field. Without the
					// fix, these reads race with SpawnFull's writes.
					_ = w.Status
					_ = w.SessionID
					_ = w.WorktreePath
				}
			}
		}()
	}

	var spawnWG sync.WaitGroup
	for i := 0; i < spawners; i++ {
		spawnWG.Add(1)
		go func(i int) {
			defer spawnWG.Done()
			for j := 0; j < iterations; j++ {
				_, _ = mgr.SpawnFull(context.Background(), SpawnRequest{
					ParentSessionID: "p-1",
					Title:           fmt.Sprintf("task-%d-%d", i, j),
					AgentID:         "agent-1",
				})
			}
		}(i)
	}

	spawnWG.Wait()
	close(stop)
	readerWG.Wait()
}
