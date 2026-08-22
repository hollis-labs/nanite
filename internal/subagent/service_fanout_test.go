package subagent

// service_fanout_test.go — G3 fan-out semaphore tests (CW-20260426-0003)
//
// Nine cases:
//  1. Under-cap parallelism: N=3 spawns complete in ≈ slowest single, not sum.
//  2. At-cap with queueing: N=5 with cap=3; first 3 run concurrently, last 2
//     queue; total ≈ 2× single duration.
//  3. Context cancellation while queued: fill cap, then cancel a queued spawn;
//     it returns cleanly without consuming a slot.
//  4. Capacity error distinguishable: fill cap, then timeout a queued spawn;
//     returns ErrSpawnFanoutCapReached (CW-20260816-0001), not bare ctx.Err().
//  5. Concurrent approvals obey the same cap as direct spawns.
//  6. Operator cancellation stops a running-observable run while it is queued.
//  7. Approval cannot expose running before its cancellation owner is installed.
//  8. Request cancellation after approval transition does not strand the run.
//  9. Concurrent duplicate approvals/cancellations create at most one runner.

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

// controlledRunner allows tests to govern when each Run call starts, blocks,
// and returns. It uses a gate channel to hold each invocation until the test
// releases it. inFlight tracks how many Run calls are concurrently executing.
type controlledRunner struct {
	// gate is closed by the test to release all blocked Run calls at once.
	// Each individual call blocks on gate until it's closed.
	gate chan struct{}
	once sync.Once

	// started is incremented when Run begins executing (before blocking).
	started atomic.Int64
	// inFlight is the high-water concurrent count — the test reads it after
	// the wave completes to assert actual parallelism.
	inFlightMu sync.Mutex
	current    int
	hwm        int // high-water mark of concurrent in-flight count
}

func newControlledRunner() *controlledRunner {
	return &controlledRunner{gate: make(chan struct{})}
}

// release unblocks all currently waiting Run invocations.
func (r *controlledRunner) release() { r.once.Do(func() { close(r.gate) }) }

// highWaterMark returns the peak concurrent count observed across all Run calls.
func (r *controlledRunner) highWaterMark() int {
	r.inFlightMu.Lock()
	defer r.inFlightMu.Unlock()
	return r.hwm
}

func (r *controlledRunner) Run(ctx context.Context, run *Run) (*Result, error) {
	r.started.Add(1)

	r.inFlightMu.Lock()
	r.current++
	if r.current > r.hwm {
		r.hwm = r.current
	}
	r.inFlightMu.Unlock()

	defer func() {
		r.inFlightMu.Lock()
		r.current--
		r.inFlightMu.Unlock()
	}()

	select {
	case <-r.gate:
		return &Result{Summary: "ok-" + run.ID, ResultJSON: `{"ok":true}`}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// spawnAsync dispatches a Spawn in a goroutine and sends (runID, error)
// onto the returned channel when Spawn returns. Useful for testing
// non-blocking spawn fan-out.
func spawnAsync(t *testing.T, svc *Service, ctx context.Context, n int) <-chan string {
	t.Helper()
	ch := make(chan string, n)
	for i := range n {
		i := i
		go func() {
			id, err := svc.Spawn(ctx, SpawnRequest{
				ParentSessionID: "sess-fanout",
				ParentAgentID:   "parent-agent",
				Role:            "worker-role",
				Prompt:          "task " + string(rune('A'+i)),
				Mode:            ModeAsync,
			})
			if err != nil {
				t.Logf("spawn %d err: %v", i, err)
				ch <- ""
				return
			}
			ch <- id
		}()
	}
	return ch
}

// runningRunSink reports the ID of every run whose running transition is
// emitted. Tests install it only after the fan-out cap is full, so the next ID
// observed belongs to the run queued behind the cap.
type runningRunSink struct {
	runIDs chan string
}

func (s *runningRunSink) SubagentStatusChanged(_ string, payload []byte) {
	var event struct {
		RunID  string `json:"run_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(payload, &event); err != nil || event.Status != StatusRunning {
		return
	}
	select {
	case s.runIDs <- event.RunID:
	default:
	}
}

type cancellingRunningSink struct {
	cancel context.CancelFunc
}

func (s *cancellingRunningSink) SubagentStatusChanged(_ string, payload []byte) {
	var event struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(payload, &event); err == nil && event.Status == StatusRunning {
		s.cancel()
	}
}

// TestFanout_UnderCap_RunsConcurrently dispatches 3 spawns (= cap) with
// a controlled runner. Releases all at once and asserts wall-clock
// elapsed ≤ 1.3× the single-slot duration (they ran in parallel, not
// serial). Also asserts the peak in-flight count reached 3.
func TestFanout_UnderCap_RunsConcurrently(t *testing.T) {
	db, _ := newTestDB(t)
	// SQLite allows only one writer at a time. Without capping the pool
	// to 1, concurrent insertRun calls race for the write lock and get
	// SQLITE_BUSY. The semaphore limits in-flight runners; the DB cap
	// serializes the insert phase (which is fast and outside the timed
	// hot path).
	db.SetMaxOpenConns(1)
	runner := newControlledRunner()
	svc := NewService(db, runner, &stubPoster{}, nil, stubSettings{})

	const n = 3
	// Each Run will block ~50ms after the gate opens. Use a generous
	// sleep so the goroutines genuinely overlap.
	holdDur := 50 * time.Millisecond

	ctx := context.Background()

	// Launch n async spawns. Because n == cap, all should acquire slots
	// immediately and proceed to Run concurrently.
	idCh := spawnAsync(t, svc, ctx, n)

	// Wait until all runners have started.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runner.started.Load() == int64(n) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if runner.started.Load() != int64(n) {
		t.Fatalf("only %d of %d runners started before deadline", runner.started.Load(), n)
	}

	// The gate is still closed; they're all blocked concurrently right now.
	// We know high-water mark is 3.
	if hw := runner.highWaterMark(); hw != n {
		t.Errorf("high-water concurrent count = %d, want %d", hw, n)
	}

	// Release them and time how long the tail takes.
	start := time.Now()
	runner.release()

	// Drain all IDs.
	ids := make([]string, 0, n)
	for range n {
		select {
		case id := <-idCh:
			if id != "" {
				ids = append(ids, id)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("spawn did not return within 3s after release")
		}
	}

	// Wait for all runners to finish (they're async; poster will eventually fire).
	elapsed := time.Since(start)

	// All three ran concurrently, so total elapsed ≈ 0 extra (gate was already
	// closed by the time Spawn returned). Generous upper bound: 1.3× holdDur.
	if elapsed > time.Duration(float64(holdDur)*1.3) {
		t.Errorf("elapsed %v > 1.3× holdDur %v — spawns may have serialized", elapsed, holdDur)
	}
	_ = ids
}

// TestFanout_AtCap_QueuesExtra dispatches 5 spawns against a cap-3 semaphore.
// The first 3 should start immediately; the 4th and 5th must queue. After
// the first wave finishes, the second wave runs. Total elapsed ≈ 2×
// single-run duration.
func TestFanout_AtCap_QueuesExtra(t *testing.T) {
	db, _ := newTestDB(t)
	db.SetMaxOpenConns(1) // serialize SQLite writes; see UnderCap comment
	runner := newControlledRunner()
	svc := NewService(db, runner, &stubPoster{}, nil, stubSettings{})

	const (
		total = 5
		cap3  = 3
	)
	ctx := context.Background()

	idCh := spawnAsync(t, svc, ctx, total)

	// Wait for exactly cap3 runners to start (the semaphore holds back the rest).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runner.started.Load() == int64(cap3) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if runner.started.Load() != int64(cap3) {
		t.Fatalf("expected %d runners started (first wave), got %d",
			cap3, runner.started.Load())
	}

	// The remaining (total-cap3) spawns are queued waiting for a slot.
	// Release the first wave.
	runner.release()

	// Wait for the second wave to start.
	deadline2 := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline2) {
		if runner.started.Load() == int64(total) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if runner.started.Load() != int64(total) {
		t.Fatalf("expected all %d runners to have started; only %d did",
			total, runner.started.Load())
	}

	// Drain all spawn calls.
	ids := make([]string, 0, total)
	for range total {
		select {
		case id := <-idCh:
			ids = append(ids, id)
		case <-time.After(3 * time.Second):
			t.Fatal("spawn did not return within 3s")
		}
	}

	// Expect all IDs non-empty.
	for i, id := range ids {
		if id == "" {
			t.Errorf("spawn %d returned empty id (error path)", i)
		}
	}
}

// TestFanout_CancelWhileQueued fills the semaphore cap, then dispatches
// one extra spawn with a cancel-able context. Cancels that context before
// any slot becomes free and verifies:
//  1. The queued spawn returns promptly with a non-nil error.
//  2. The cap remains fully utilised by the original 3 (none leaked).
//  3. The 3 originals complete normally after release.
func TestFanout_CancelWhileQueued(t *testing.T) {
	db, _ := newTestDB(t)
	db.SetMaxOpenConns(1) // serialize SQLite writes; see UnderCap comment
	runner := newControlledRunner()
	svc := NewService(db, runner, &stubPoster{}, nil, stubSettings{})

	const cap3 = 3
	ctx := context.Background()

	// Fill all cap3 slots.
	fillCh := spawnAsync(t, svc, ctx, cap3)

	// Wait until all cap3 runners are in-flight (slots exhausted).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runner.started.Load() == int64(cap3) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if runner.started.Load() != int64(cap3) {
		t.Fatalf("first %d runners did not start within deadline", cap3)
	}

	// Dispatch one extra spawn with a context we'll cancel.
	runningIDs := make(chan string, 1)
	svc.SetStreamSink(&runningRunSink{runIDs: runningIDs})
	cancelCtx, cancel := context.WithCancel(context.Background())
	extraDone := make(chan error, 1)
	go func() {
		_, err := svc.Spawn(cancelCtx, SpawnRequest{
			ParentSessionID: "sess-fanout",
			ParentAgentID:   "parent-agent",
			Role:            "worker-role",
			Prompt:          "queued-extra",
			Mode:            ModeAsync,
		})
		extraDone <- err
	}()

	var queuedRunID string
	select {
	case queuedRunID = <-runningIDs:
	case <-time.After(2 * time.Second):
		t.Fatal("queued run did not publish running status")
	}

	// Cancel the queued spawn's context.
	cancel()

	// It must return promptly with an error (context.Canceled or similar).
	select {
	case err := <-extraDone:
		if err == nil {
			t.Error("expected non-nil error from cancelled queued spawn, got nil")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("cancelled queued spawn did not return within 500ms")
	}

	// The extra spawn must NOT have started the runner — slot was never acquired.
	if runner.started.Load() != int64(cap3) {
		t.Errorf("runner.started = %d, want %d — cancelled spawn consumed a slot",
			runner.started.Load(), cap3)
	}
	run, err := svc.Status(context.Background(), queuedRunID)
	if err != nil {
		t.Fatalf("status cancelled queued run: %v", err)
	}
	if run.Status != StatusCancelled || run.CompletedAt == "" {
		t.Fatalf("cancelled queued run = status %q completed_at %q; want cancelled terminal row",
			run.Status, run.CompletedAt)
	}

	// Release the original cap3 and let them finish.
	runner.release()

	for range cap3 {
		select {
		case <-fillCh:
		case <-time.After(3 * time.Second):
			t.Fatal("original spawn did not return within 3s after release")
		}
	}
}

// TestFanout_CapacityErrorDistinguishable saturates all 3 slots, then
// dispatches a 4th spawn with a short timeout. Verifies that the error
// returned is ErrSpawnFanoutCapReached (CW-20260816-0001), not a bare
// context.DeadlineExceeded, so the MCP layer can map it to
// error.kind="at_capacity" instead of a generic timeout.
func TestFanout_CapacityErrorDistinguishable(t *testing.T) {
	db, _ := newTestDB(t)
	db.SetMaxOpenConns(1) // serialize SQLite writes
	runner := newControlledRunner()
	svc := NewService(db, runner, &stubPoster{}, nil, stubSettings{})

	const cap3 = 3
	ctx := context.Background()

	// Fill all cap3 slots with long-running spawns.
	fillCh := spawnAsync(t, svc, ctx, cap3)

	// Wait until all cap3 runners are in-flight.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runner.started.Load() == int64(cap3) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if runner.started.Load() != int64(cap3) {
		t.Fatalf("first %d runners did not start within deadline", cap3)
	}

	// Dispatch a 4th spawn with a short timeout while all slots are occupied.
	runningIDs := make(chan string, 1)
	svc.SetStreamSink(&runningRunSink{runIDs: runningIDs})
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := svc.Spawn(timeoutCtx, SpawnRequest{
		ParentSessionID: "sess-fanout",
		ParentAgentID:   "parent-agent",
		Role:            "worker-role",
		Prompt:          "capacity-blocked",
		Mode:            ModeAsync,
	})

	// Must return an error (all slots occupied, context timed out).
	if err == nil {
		t.Fatal("expected error from spawn when at capacity, got nil")
	}

	// The error must be the distinguishable ErrSpawnFanoutCapReached,
	// not a bare context.DeadlineExceeded.
	if !errors.Is(err, ErrSpawnFanoutCapReached) {
		t.Errorf("expected errors.Is(err, ErrSpawnFanoutCapReached), got: %v", err)
	}
	var queuedRunID string
	select {
	case queuedRunID = <-runningIDs:
	default:
		t.Fatal("capacity-blocked run did not publish running status")
	}
	run, statusErr := svc.Status(context.Background(), queuedRunID)
	if statusErr != nil {
		t.Fatalf("status capacity-blocked run: %v", statusErr)
	}
	if run.Status != StatusCancelled || run.CompletedAt == "" {
		t.Fatalf("capacity-blocked run = status %q completed_at %q; want cancelled terminal row",
			run.Status, run.CompletedAt)
	}

	// The 4th spawn must NOT have started the runner.
	if runner.started.Load() != int64(cap3) {
		t.Errorf("runner.started = %d, want %d — capacity-blocked spawn consumed a slot",
			runner.started.Load(), cap3)
	}

	// Release the original cap3 and let them finish.
	runner.release()

	for range cap3 {
		select {
		case <-fillCh:
		case <-time.After(3 * time.Second):
			t.Fatal("original spawn did not return within 3s after release")
		}
	}
}

func TestFanout_ApproveObeysConcurrencyCap(t *testing.T) {
	db, _ := newTestDB(t)
	db.SetMaxOpenConns(1)
	runner := newControlledRunner()
	defer runner.release()
	settings := stubSettings{us: store.UserSettings{
		SubagentApprovalRequired:       true,
		SubagentApprovalTimeoutSeconds: 3600,
	}}
	svc := NewService(db, runner, &stubPoster{}, &stubEmitter{}, settings)

	const total = 5
	runIDs := make([]string, 0, total)
	for i := range total {
		runID, err := svc.Spawn(context.Background(), SpawnRequest{
			ParentSessionID: "sess-approve-fanout",
			ParentAgentID:   "parent-agent",
			Role:            "worker-role",
			Prompt:          "approval " + string(rune('A'+i)),
			Mode:            ModeAsync,
		})
		if err != nil {
			t.Fatalf("spawn gated run %d: %v", i, err)
		}
		runIDs = append(runIDs, runID)
	}

	approveErrs := make(chan error, total)
	for _, runID := range runIDs {
		runID := runID
		go func() {
			approveErrs <- svc.Approve(context.Background(), runID)
		}()
	}
	for range total {
		if err := <-approveErrs; err != nil {
			t.Fatalf("approve: %v", err)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && runner.started.Load() < spawnFanoutCap {
		time.Sleep(5 * time.Millisecond)
	}
	if got := runner.started.Load(); got != spawnFanoutCap {
		t.Fatalf("runners started before release = %d, want exactly %d", got, spawnFanoutCap)
	}

	// Give dispatches that incorrectly bypass the semaphore enough time to
	// enter Run; the gate remains closed throughout this observation window.
	time.Sleep(100 * time.Millisecond)
	if got := runner.started.Load(); got != spawnFanoutCap {
		t.Fatalf("runners started while cap held = %d, want %d", got, spawnFanoutCap)
	}
	if got := runner.highWaterMark(); got > spawnFanoutCap {
		t.Fatalf("runner high-water mark = %d, exceeds cap %d", got, spawnFanoutCap)
	}

	runner.release()
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && runner.started.Load() < total {
		time.Sleep(5 * time.Millisecond)
	}
	if got := runner.started.Load(); got != total {
		t.Fatalf("runners started after release = %d, want %d", got, total)
	}
}

func TestFanout_OperatorCancelWhileQueuedPreventsRunner(t *testing.T) {
	db, _ := newTestDB(t)
	db.SetMaxOpenConns(1)
	runner := newControlledRunner()
	defer runner.release()
	svc := NewService(db, runner, &stubPoster{}, nil, stubSettings{})

	fillCh := spawnAsync(t, svc, context.Background(), spawnFanoutCap)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && runner.started.Load() < spawnFanoutCap {
		time.Sleep(5 * time.Millisecond)
	}
	if got := runner.started.Load(); got != spawnFanoutCap {
		t.Fatalf("runners started while filling cap = %d, want %d", got, spawnFanoutCap)
	}

	runningIDs := make(chan string, 1)
	svc.SetStreamSink(&runningRunSink{runIDs: runningIDs})
	extraDone := make(chan error, 1)
	go func() {
		_, err := svc.Spawn(context.Background(), SpawnRequest{
			ParentSessionID: "sess-fanout",
			ParentAgentID:   "parent-agent",
			Role:            "worker-role",
			Prompt:          "operator-cancelled-while-queued",
			Mode:            ModeAsync,
		})
		extraDone <- err
	}()

	var queuedRunID string
	select {
	case queuedRunID = <-runningIDs:
	case <-time.After(2 * time.Second):
		t.Fatal("queued run did not emit its running transition")
	}
	if err := svc.Cancel(context.Background(), queuedRunID); err != nil {
		t.Fatalf("cancel queued run: %v", err)
	}

	select {
	case <-extraDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("operator-cancelled queued spawn did not unblock promptly")
	}
	if got := runner.started.Load(); got != spawnFanoutCap {
		t.Fatalf("runner.started = %d, want %d; cancelled queued runner executed", got, spawnFanoutCap)
	}

	runner.release()
	for range spawnFanoutCap {
		select {
		case <-fillCh:
		case <-time.After(3 * time.Second):
			t.Fatal("original spawn did not return after release")
		}
	}

	// Leave enough time for an incorrectly queued runner to acquire the freed
	// slot and enter Run before checking the durable terminal state.
	time.Sleep(100 * time.Millisecond)
	if got := runner.started.Load(); got != spawnFanoutCap {
		t.Fatalf("runner.started after release = %d, want %d", got, spawnFanoutCap)
	}
	run, err := svc.Status(context.Background(), queuedRunID)
	if err != nil {
		t.Fatalf("status queued run: %v", err)
	}
	if run.Status != StatusCancelled {
		t.Fatalf("queued run status = %q, want %q", run.Status, StatusCancelled)
	}
}

// TestApprove_HandoffPrecedesRunningTransition holds the handoff mutex until
// the request context expires. The historical implementation transitioned the
// row before trying to take this mutex, leaving a durable running row with no
// cancellation owner. The fixed implementation cannot make that transition.
func TestApprove_HandoffPrecedesRunningTransition(t *testing.T) {
	db, _ := newTestDB(t)
	runner := newControlledRunner()
	defer runner.release()
	settings := stubSettings{us: store.UserSettings{
		SubagentApprovalRequired:       true,
		SubagentApprovalTimeoutSeconds: 3600,
	}}
	svc := NewService(db, runner, &stubPoster{}, &stubEmitter{}, settings)
	runID, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-approve-handoff",
		ParentAgentID:   "parent-agent",
		Role:            "worker-role",
		Prompt:          "handoff barrier",
		Mode:            ModeAsync,
	})
	if err != nil {
		t.Fatalf("spawn gated run: %v", err)
	}

	svc.cancelMu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	approveDone := make(chan error, 1)
	go func() { approveDone <- svc.Approve(ctx, runID) }()
	<-ctx.Done()

	// Read directly while the handoff mutex is still held. A running status
	// here would prove the durable transition escaped cancellation ownership.
	run, statusErr := svc.Status(context.Background(), runID)
	svc.cancelMu.Unlock()
	if statusErr != nil {
		t.Fatalf("status while handoff blocked: %v", statusErr)
	}
	if run.Status != StatusRequested {
		t.Fatalf("status while handoff blocked = %q, want %q", run.Status, StatusRequested)
	}
	select {
	case approveErr := <-approveDone:
		if !errors.Is(approveErr, context.DeadlineExceeded) {
			t.Fatalf("approve error = %v, want context deadline exceeded", approveErr)
		}
	case <-time.After(time.Second):
		t.Fatal("approve did not return after handoff mutex released")
	}
	run, err = svc.Status(context.Background(), runID)
	if err != nil {
		t.Fatalf("final status: %v", err)
	}
	if run.Status != StatusRequested {
		t.Fatalf("final status = %q, want %q", run.Status, StatusRequested)
	}
	if got := runner.started.Load(); got != 0 {
		t.Fatalf("runner.started = %d, want 0", got)
	}
}

func TestApprove_RequestCancellationAfterTransitionRetainsOwner(t *testing.T) {
	db, _ := newTestDB(t)
	runner := newControlledRunner()
	defer runner.release()
	settings := stubSettings{us: store.UserSettings{
		SubagentApprovalRequired:       true,
		SubagentApprovalTimeoutSeconds: 3600,
	}}
	svc := NewService(db, runner, &stubPoster{}, &stubEmitter{}, settings)
	runID, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-approve-request-cancel",
		ParentAgentID:   "parent-agent",
		Role:            "worker-role",
		Prompt:          "cancel request after transition",
		Mode:            ModeAsync,
	})
	if err != nil {
		t.Fatalf("spawn gated run: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	svc.SetStreamSink(&cancellingRunningSink{cancel: cancel})
	if err := svc.Approve(ctx, runID); err != nil {
		t.Fatalf("approve after synchronous request cancellation: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("running transition did not cancel request context")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && runner.started.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	if got := runner.started.Load(); got != 1 {
		t.Fatalf("runner.started = %d, want 1; request cancellation escaped into background run", got)
	}
	if err := svc.Cancel(context.Background(), runID); err != nil {
		t.Fatalf("operator cancel after request ended: %v", err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		run, statusErr := svc.Status(context.Background(), runID)
		if statusErr == nil && run.Status == StatusCancelled && len(svc.spawnSem) == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	run, _ := svc.Status(context.Background(), runID)
	t.Fatalf("run did not settle after operator cancel: status=%q slots=%d", run.Status, len(svc.spawnSem))
}

func TestApprove_ConcurrentDuplicateApproveCancelSingleOwner(t *testing.T) {
	db, _ := newTestDB(t)
	db.SetMaxOpenConns(1)
	runner := newControlledRunner()
	defer runner.release()
	settings := stubSettings{us: store.UserSettings{
		SubagentApprovalRequired:       true,
		SubagentApprovalTimeoutSeconds: 3600,
	}}
	svc := NewService(db, runner, &stubPoster{}, &stubEmitter{}, settings)
	runID, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-approve-race",
		ParentAgentID:   "parent-agent",
		Role:            "worker-role",
		Prompt:          "duplicate approve and cancel",
		Mode:            ModeAsync,
	})
	if err != nil {
		t.Fatalf("spawn gated run: %v", err)
	}

	const callers = 8
	start := make(chan struct{})
	approveErrs := make(chan error, callers)
	cancelErrs := make(chan error, callers)
	for range callers {
		go func() {
			<-start
			approveErrs <- svc.Approve(context.Background(), runID)
		}()
		go func() {
			<-start
			cancelErrs <- svc.Cancel(context.Background(), runID)
		}()
	}
	close(start)

	approved := 0
	for range callers {
		approveErr := <-approveErrs
		switch {
		case approveErr == nil:
			approved++
		case errors.Is(approveErr, ErrNotPending):
		default:
			t.Fatalf("unexpected approve error: %v", approveErr)
		}
		if cancelErr := <-cancelErrs; cancelErr != nil {
			t.Fatalf("cancel: %v", cancelErr)
		}
	}
	if approved > 1 {
		t.Fatalf("successful approvals = %d, want at most 1", approved)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		svc.cancelMu.Lock()
		_, owned := svc.cancelers[runID]
		svc.cancelMu.Unlock()
		if !owned && len(svc.spawnSem) == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	run, err := svc.Status(context.Background(), runID)
	if err != nil {
		t.Fatalf("final status: %v", err)
	}
	if run.Status != StatusCancelled {
		t.Fatalf("final status = %q, want %q", run.Status, StatusCancelled)
	}
	if got := runner.started.Load(); got > 1 {
		t.Fatalf("runner.started = %d, want at most 1", got)
	}
	svc.cancelMu.Lock()
	_, owned := svc.cancelers[runID]
	svc.cancelMu.Unlock()
	if owned {
		t.Fatal("cancellation owner leaked after concurrent approvals/cancels")
	}
	if got := len(svc.spawnSem); got != 0 {
		t.Fatalf("spawn slots held = %d, want 0", got)
	}
}
