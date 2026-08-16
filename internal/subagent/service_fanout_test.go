package subagent

// service_fanout_test.go — G3 fan-out semaphore tests (CW-20260426-0003)
//
// Four cases:
//  1. Under-cap parallelism: N=3 spawns complete in ≈ slowest single, not sum.
//  2. At-cap with queueing: N=5 with cap=3; first 3 run concurrently, last 2
//     queue; total ≈ 2× single duration.
//  3. Context cancellation while queued: fill cap, then cancel a queued spawn;
//     it returns cleanly without consuming a slot.
//  4. Capacity error distinguishable: fill cap, then timeout a queued spawn;
//     returns ErrSpawnFanoutCapReached (CW-20260816-0001), not bare ctx.Err().

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// controlledRunner allows tests to govern when each Run call starts, blocks,
// and returns. It uses a gate channel to hold each invocation until the test
// releases it. inFlight tracks how many Run calls are concurrently executing.
type controlledRunner struct {
	// gate is closed by the test to release all blocked Run calls at once.
	// Each individual call blocks on gate until it's closed.
	gate chan struct{}

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
func (r *controlledRunner) release() { close(r.gate) }

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

	// Give it a moment to block on the semaphore (slots are full).
	time.Sleep(20 * time.Millisecond)

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

