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

// shutdownBarrierDelegator models a provider call that is genuinely in
// flight when Shutdown arrives, and gives the test three barriers to
// synchronize on instead of a fixed sleep:
//
//   - entered   — closed once DelegateTask is actually executing, i.e. once
//     SpawnFull is past its whole spawn phase (worker published, heartbeat
//     registered, SetStatus(StatusRunning) done).
//   - cancelled — closed once the delegation has observed its context being
//     cancelled, proving Shutdown really cancelled it.
//   - release   — supplied by the test; the delegation parks here until the
//     test lets it unwind, so SpawnFull's error path runs at a point the
//     test controls rather than at a point the scheduler picks.
//
// It is deliberately separate from manager_shutdown_test.go's
// blockingDelegator, which owns the drain-within-budget property and must
// keep returning as soon as its context is cancelled.
type shutdownBarrierDelegator struct {
	entered   chan struct{}
	cancelled chan struct{}
	release   chan struct{}

	enteredOnce   sync.Once
	cancelledOnce sync.Once
}

func (d *shutdownBarrierDelegator) DelegateTask(ctx context.Context, _ chat.DelegationRequest) (*chat.DelegationResult, error) {
	d.enteredOnce.Do(func() { close(d.entered) })
	<-ctx.Done()
	d.cancelledOnce.Do(func() { close(d.cancelled) })
	<-d.release
	return nil, ctx.Err()
}

// TestShutdown pins the terminal state of a worker that Shutdown interrupts
// mid-delegation: it is "cancelled", and nothing downstream may downgrade it
// to "failed".
//
// That is the manager's stated intent — SpawnFull guards every one of its
// own status writes with `if w.GetStatus() != StatusCancelled` precisely so a
// concurrent Shutdown/Cancel wins (see manager.go, "Respect that terminal
// state rather than overwriting it"). "failed" is not a second legal outcome
// here, so the assertion below is exact and must not be widened to accept
// both.
//
// This test previously waited with time.Sleep(50 * time.Millisecond), which
// is not a barrier: it bounds elapsed wall-clock time, not SpawnFull's
// progress. On a saturated machine — a full-repo `-race` run on one CI
// runner — the spawned goroutine can still be inside its spawn phase when
// the sleep expires. Shutdown then marks a worker "cancelled" that SpawnFull
// immediately overwrites with StatusRunning; the already-cancelled context
// makes DelegateTask fail, the != StatusCancelled guard sees "running", and
// the worker settles on "failed".
//
// The entered-barrier is therefore load-bearing, not decoration. To confirm
// that after changing this test, replace `<-deleg.entered` below with a spin
// on `len(mgr.List()) == 0` — the strictly weaker guarantee an expired sleep
// gives you — and loop the body: the status assertion starts failing. Doing
// exactly that at d5de0ba1 failed 43 times in 200 iterations, versus 0 in 200
// with the barrier; re-derive rather than trusting those figures.
func TestShutdown(t *testing.T) {
	deleg := &shutdownBarrierDelegator{
		entered:   make(chan struct{}),
		cancelled: make(chan struct{}),
		release:   make(chan struct{}),
	}
	mgr := newTestManager(deleg)

	var releaseOnce sync.Once
	releaseDelegation := func() { releaseOnce.Do(func() { close(deleg.release) }) }
	// Guarantees the spawned goroutine unwinds even on an early t.Fatal.
	t.Cleanup(releaseDelegation)

	spawnDone := make(chan struct{})
	go func() {
		defer close(spawnDone)
		_, _ = mgr.SpawnFull(context.Background(), SpawnRequest{
			ParentSessionID: "p-1",
			Title:           "long task",
			AgentID:         "agent-1",
		})
	}()

	// Barrier, not a sleep. The timeout is a liveness backstop only; the
	// happy path never waits on it.
	select {
	case <-deleg.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("delegation never started; Shutdown would not have exercised cancellation")
	}

	// Shutdown's own status write happens synchronously before it waits on
	// its lifecycle, so by the time this returns the transition has landed.
	// The drain-within-budget property belongs to
	// TestShutdown_CancelsInFlightWorkAndDrains, not here.
	_ = mgr.Shutdown(2 * time.Second)

	select {
	case <-deleg.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not cancel the in-flight delegation's context")
	}

	// The delegation is parked on release, so SpawnFull cannot have written
	// any status yet. List returns value snapshots, so field reads here are
	// safe without further synchronization.
	//
	// Assert the worker is actually present: the old loop iterated over
	// whatever List returned and passed vacuously when that was empty, which
	// under load it sometimes was.
	workers := mgr.List()
	if len(workers) != 1 {
		t.Fatalf("after Shutdown: List() = %d workers, want 1", len(workers))
	}
	if got := workers[0].Status; got != StatusCancelled {
		t.Errorf("after Shutdown: worker %s status = %q, want %q",
			workers[0].ID[:8], got, StatusCancelled)
	}

	// Now let the delegation unwind. SpawnFull's error path must respect the
	// cancelled terminal state rather than downgrading it to "failed".
	releaseDelegation()
	select {
	case <-spawnDone:
	case <-time.After(5 * time.Second):
		t.Fatal("SpawnFull did not return after its context was cancelled")
	}

	workers = mgr.List()
	if len(workers) != 1 {
		t.Fatalf("after SpawnFull returned: List() = %d workers, want 1", len(workers))
	}
	if got := workers[0].Status; got != StatusCancelled {
		t.Errorf("after SpawnFull returned: worker %s status = %q, want %q (SpawnFull's error path must not overwrite a cancelled worker)",
			workers[0].ID[:8], got, StatusCancelled)
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
