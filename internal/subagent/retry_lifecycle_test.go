package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// CW-20260519-0075 — checkpoint/resume + retry lifecycle (audit §P6).
//
// These tests pin the loop-in-execute retry/resume behavior. They focus
// on the package-internal contract:
//
//   - Retriable outcomes (over_budget, stalled, failed-with-OnFail=retry)
//     trigger another iteration up to max_retries
//   - Unretriable outcomes (fabrication-suspected, cancelled, rejected)
//     never auto-retry regardless of budget
//   - Each attempt's outcome is folded into AttemptsJSON
//   - The runner sees a stable Run struct across attempts (the
//     ChildSessionID-skip behavior is exercised in the chat-runner test
//     file in internal/service)
//   - OnFail=block / OnFail=escalate suppress retry for the
//     failed/stalled buckets but do not affect over_budget
//   - Cancel observed mid-chain bails out of the loop

// countingRunner returns a programmable sequence of outcomes, one per
// invocation. The slice length must >= the number of expected attempts;
// extra calls beyond the slice return the last outcome verbatim. Records
// each Run call's Run pointer so tests can assert ID stability /
// ChildSessionID propagation across attempts.
type countingRunner struct {
	mu       sync.Mutex
	outcomes []runOutcome
	calls    []*Run
}

type runOutcome struct {
	result *Result
	err    error
}

func (r *countingRunner) Run(_ context.Context, run *Run) (*Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Record a snapshot of the input Run pointer so the test can assert
	// the per-attempt state the runner saw.
	r.calls = append(r.calls, run)
	idx := len(r.calls) - 1
	if idx >= len(r.outcomes) {
		idx = len(r.outcomes) - 1
	}
	return r.outcomes[idx].result, r.outcomes[idx].err
}

func (r *countingRunner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// failOutcome builds a generic failure outcome with no partial result —
// the classifier sees runCtx not expired + no ErrStalled sentinel and
// stamps StatusFailed.
func failOutcome() runOutcome {
	return runOutcome{nil, errors.New("runner blew up")}
}

// stallOutcome builds an outcome whose error wraps ErrStalled so the
// classifier picks StatusStalled.
func stallOutcome() runOutcome {
	return runOutcome{
		&Result{ResultJSON: `{"partial":true,"summary":"","tools":{"calls":0,"results_success":0,"results_error":0}}`},
		errors.Join(errors.New("stalled"), ErrStalled),
	}
}

// productiveOutcome builds a successful outcome.
func productiveOutcome(summary string) runOutcome {
	return runOutcome{
		&Result{Summary: summary, ResultJSON: fmt.Sprintf(`{"summary":%q}`, summary)},
		nil,
	}
}

// TestExecute_RetryExhaustionTerminatesAsFailed pins the cap behavior:
// a runner that keeps failing must eventually terminate with the
// final terminal status surfaced on the row, retry_count == max_retries,
// and attempts_json carrying the prior attempts.
func TestExecute_RetryExhaustionTerminatesAsFailed(t *testing.T) {
	db, _ := newTestDB(t)
	runner := &countingRunner{
		outcomes: []runOutcome{failOutcome(), failOutcome(), failOutcome(), failOutcome()},
	}
	svc := NewService(db, runner, &stubPoster{}, nil, stubSettings{})

	id, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1",
		ParentAgentID:   "file-backend",
		Role:            "file-summarizer",
		Prompt:          "p",
		Mode:            ModeSync,
		MaxRetries:      3,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got := runner.callCount(); got != 4 {
		t.Fatalf("runner call count = %d, want 4 (1 initial + 3 retries)", got)
	}

	run, err := svc.Status(context.Background(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if run.Status != StatusFailed {
		t.Errorf("Status = %q, want %q", run.Status, StatusFailed)
	}
	if run.RetryCount != 3 {
		t.Errorf("RetryCount = %d, want 3", run.RetryCount)
	}
	if run.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", run.MaxRetries)
	}
	// attempts_json must contain the first 3 attempts (the 4th is in
	// the top-level columns).
	var attempts []attemptRecord
	if err := json.Unmarshal([]byte(run.AttemptsJSON), &attempts); err != nil {
		t.Fatalf("attempts_json malformed: %v\nblob=%s", err, run.AttemptsJSON)
	}
	if len(attempts) != 3 {
		t.Errorf("attempts_json length = %d, want 3", len(attempts))
	}
	for i, a := range attempts {
		if a.Status != StatusFailed {
			t.Errorf("attempt[%d].status = %q, want failed", i, a.Status)
		}
	}
}

// TestExecute_RetrySucceedsBeforeBudget pins the eventual-success path:
// a runner that fails then succeeds must end in StatusCompleted with
// retry_count reflecting how many retries happened.
func TestExecute_RetrySucceedsBeforeBudget(t *testing.T) {
	db, _ := newTestDB(t)
	runner := &countingRunner{
		outcomes: []runOutcome{
			failOutcome(),
			failOutcome(),
			productiveOutcome("eventually finished"),
		},
	}
	svc := NewService(db, runner, &stubPoster{}, nil, stubSettings{})

	id, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1",
		ParentAgentID:   "file-backend",
		Role:            "file-summarizer",
		Prompt:          "p",
		Mode:            ModeSync,
		MaxRetries:      3,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got := runner.callCount(); got != 3 {
		t.Fatalf("runner call count = %d, want 3 (1 initial + 2 retries, then success)", got)
	}

	run, err := svc.Status(context.Background(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if run.Status != StatusCompleted {
		t.Errorf("Status = %q, want %q", run.Status, StatusCompleted)
	}
	if run.RetryCount != 2 {
		t.Errorf("RetryCount = %d, want 2 (two retries before success)", run.RetryCount)
	}
	// attempts_json carries the two failed attempts; the final
	// successful attempt is on the top-level columns.
	var attempts []attemptRecord
	if err := json.Unmarshal([]byte(run.AttemptsJSON), &attempts); err != nil {
		t.Fatalf("attempts_json malformed: %v", err)
	}
	if len(attempts) != 2 {
		t.Errorf("attempts_json length = %d, want 2", len(attempts))
	}
}

// TestExecute_OnFailBlockSuppressesRetry pins the routing-on-fail
// policy: on_fail=block must NOT trigger retries on failed/stalled
// outcomes even with budget remaining.
func TestExecute_OnFailBlockSuppressesRetry(t *testing.T) {
	db, _ := newTestDB(t)
	runner := &countingRunner{
		outcomes: []runOutcome{failOutcome(), failOutcome()},
	}
	svc := NewService(db, runner, &stubPoster{}, nil, stubSettings{})

	id, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1",
		ParentAgentID:   "file-backend",
		Role:            "file-summarizer",
		Prompt:          "p",
		Mode:            ModeSync,
		MaxRetries:      3,
		OnFail:          OnFailBlock,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got := runner.callCount(); got != 1 {
		t.Errorf("runner call count = %d, want 1 (block must suppress retry)", got)
	}

	run, _ := svc.Status(context.Background(), id)
	if run.Status != StatusFailed {
		t.Errorf("Status = %q, want %q", run.Status, StatusFailed)
	}
	if run.RetryCount != 0 {
		t.Errorf("RetryCount = %d, want 0", run.RetryCount)
	}
	if run.OnFail != OnFailBlock {
		t.Errorf("persisted OnFail = %q, want %q", run.OnFail, OnFailBlock)
	}
}

// TestExecute_OnFailEscalateSuppressesRetry mirrors the block case —
// escalate behaves identically at the subagent layer.
func TestExecute_OnFailEscalateSuppressesRetry(t *testing.T) {
	db, _ := newTestDB(t)
	runner := &countingRunner{outcomes: []runOutcome{failOutcome(), failOutcome()}}
	svc := NewService(db, runner, &stubPoster{}, nil, stubSettings{})
	id, _ := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1", ParentAgentID: "p", Role: "r", Prompt: "p", Mode: ModeSync,
		MaxRetries: 3, OnFail: OnFailEscalate,
	})
	if got := runner.callCount(); got != 1 {
		t.Errorf("runner call count = %d, want 1 (escalate must suppress retry)", got)
	}
	run, _ := svc.Status(context.Background(), id)
	if run.Status != StatusFailed {
		t.Errorf("Status = %q, want failed", run.Status)
	}
}

// fabricationRunner produces the canonical fabrication-suspected error
// shape so the loop's substring filter trips and the run is NOT retried.
type fabricationRunner struct{ atomic.Int32 }

func (r *fabricationRunner) Run(_ context.Context, _ *Run) (*Result, error) {
	r.Int32.Add(1)
	return nil, errors.New("subagent: fabrication suspected — tools attempted but none succeeded, yet assistant produced non-empty text: tool_calls=2, tool_results_error=2, tool_results_success=0, assistant_text_chars=120")
}

// TestExecute_FabricationDoesNotRetry pins the unretriable-by-construction
// classification: even with on_fail=retry and budget remaining, a
// fabrication-suspected outcome must not retry.
func TestExecute_FabricationDoesNotRetry(t *testing.T) {
	db, _ := newTestDB(t)
	runner := &fabricationRunner{}
	svc := NewService(db, runner, &stubPoster{}, nil, stubSettings{})

	id, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1",
		ParentAgentID:   "file-backend",
		Role:            "file-summarizer",
		Prompt:          "p",
		Mode:            ModeSync,
		MaxRetries:      3,
		OnFail:          OnFailRetry,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got := runner.Int32.Load(); got != 1 {
		t.Errorf("runner call count = %d, want 1 (fabrication must not retry)", got)
	}
	run, _ := svc.Status(context.Background(), id)
	if run.Status != StatusFailed {
		t.Errorf("Status = %q, want failed", run.Status)
	}
	if run.RetryCount != 0 {
		t.Errorf("RetryCount = %d, want 0 (fabrication terminates immediately)", run.RetryCount)
	}
}

// TestShouldRetry_OverBudgetIgnoresOnFailBlock pins the policy directly:
// over_budget MUST be retriable within budget regardless of on_fail. The
// audit explicitly carves over_budget out of "failure" — the wall-clock
// backstop firing on a productive run must not be routed by on_fail.
//
// Tested at the shouldRetry layer rather than end-to-end because driving
// the over_budget classification end-to-end requires the per-attempt
// timeout to actually fire, and the floor (minTimeoutSeconds=60) makes
// that infeasible in a unit test. The end-to-end taxonomy test in
// service_test.go already covers the wall-clock backstop integration.
func TestShouldRetry_OverBudgetIgnoresOnFailBlock(t *testing.T) {
	db, _ := newTestDB(t)
	svc := NewService(db, EchoRunner{}, &stubPoster{}, nil, stubSettings{})

	// Insert a row so shouldRetry's row-status re-read finds something
	// other than cancelled / rejected. The values here mimic mid-chain
	// state: status is the JUST-classified terminal outcome of an
	// attempt; the persisted row is still 'running' at the point
	// shouldRetry is consulted (persistRetryCheckpoint not yet called
	// for this iteration).
	run := &Run{
		ID:              "test-run",
		ParentSessionID: "p",
		Role:            "r",
		Mode:            ModeSync,
		Status:          StatusOverBudget,
		Error:           "deadline exceeded",
		RetryCount:      0,
		MaxRetries:      2,
		OnFail:          OnFailBlock,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := svc.insertRun(context.Background(), run); err != nil {
		t.Fatalf("insertRun: %v", err)
	}
	// Bring the row into the running-while-deciding state by stamping
	// status=running directly (mirrors what persistRetryCheckpoint does
	// between attempts).
	if _, err := db.Exec(`UPDATE subagent_runs SET status='running' WHERE id=?`, run.ID); err != nil {
		t.Fatalf("update status: %v", err)
	}

	if !svc.shouldRetry(context.Background(), run, errors.New("ctx canceled")) {
		t.Error("shouldRetry(over_budget, on_fail=block) = false; want true (over_budget is not on_fail-routed)")
	}

	// Budget exhausted: no retry even for over_budget.
	run.RetryCount = run.MaxRetries
	if svc.shouldRetry(context.Background(), run, errors.New("ctx canceled")) {
		t.Error("shouldRetry(over_budget, retry_count=max_retries) = true; want false (budget cap holds for over_budget too)")
	}
}

// TestShouldRetry_FailedHonoursOnFailBlock pins the routing-on-fail
// policy at the same layer: a failed outcome with on_fail=block must
// NOT retry, even with budget remaining.
func TestShouldRetry_FailedHonoursOnFailBlock(t *testing.T) {
	db, _ := newTestDB(t)
	svc := NewService(db, EchoRunner{}, &stubPoster{}, nil, stubSettings{})
	run := &Run{
		ID:              "test-run-2",
		ParentSessionID: "p",
		Role:            "r",
		Mode:            ModeSync,
		Status:          StatusFailed,
		Error:           "boom",
		RetryCount:      0,
		MaxRetries:      3,
		OnFail:          OnFailBlock,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := svc.insertRun(context.Background(), run); err != nil {
		t.Fatalf("insertRun: %v", err)
	}
	if _, err := db.Exec(`UPDATE subagent_runs SET status='running' WHERE id=?`, run.ID); err != nil {
		t.Fatalf("update status: %v", err)
	}
	if svc.shouldRetry(context.Background(), run, errors.New("boom")) {
		t.Error("shouldRetry(failed, on_fail=block) = true; want false")
	}

	// Same scenario but on_fail=retry → retries.
	run.OnFail = OnFailRetry
	if !svc.shouldRetry(context.Background(), run, errors.New("boom")) {
		t.Error("shouldRetry(failed, on_fail=retry) = false; want true")
	}
}

// TestShouldRetry_CancelledRowAbortsChain pins the cancel-mid-chain
// invariant at the shouldRetry layer: a row whose status was flipped to
// cancelled between attempts must abort the chain rather than triggering
// another runner invocation.
func TestShouldRetry_CancelledRowAbortsChain(t *testing.T) {
	db, _ := newTestDB(t)
	svc := NewService(db, EchoRunner{}, &stubPoster{}, nil, stubSettings{})
	run := &Run{
		ID:              "test-run-3",
		ParentSessionID: "p",
		Role:            "r",
		Mode:            ModeSync,
		Status:          StatusFailed, // in-memory just-classified
		Error:           "boom",
		MaxRetries:      3,
		OnFail:          OnFailRetry,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := svc.insertRun(context.Background(), run); err != nil {
		t.Fatalf("insertRun: %v", err)
	}
	// Concurrent Cancel landed: persisted status is cancelled.
	if _, err := db.Exec(`UPDATE subagent_runs SET status='cancelled' WHERE id=?`, run.ID); err != nil {
		t.Fatalf("update status: %v", err)
	}
	if svc.shouldRetry(context.Background(), run, errors.New("boom")) {
		t.Error("shouldRetry(persisted-cancelled) = true; want false")
	}
}

// TestExecute_CancelDuringAttemptBailsOut pins the end-to-end
// cancellation discipline: a Cancel call that lands while a runner is
// in-flight cancels the runner's ctx, the runner returns ctx.Err(), and
// the loop sees the cancelled ctx and bails without retrying.
//
// The runner blocks on its ctx so the test has a deterministic window
// to issue Cancel — no race with the next-iteration entry into Run.
func TestExecute_CancelDuringAttemptBailsOut(t *testing.T) {
	db, _ := newTestDB(t)
	runner := &blockingRunner{
		started: make(chan struct{}, 8),
	}
	svc := NewService(db, runner, &stubPoster{}, nil, stubSettings{})

	runID := make(chan string, 1)
	go func() {
		id, err := svc.Spawn(context.Background(), SpawnRequest{
			ParentSessionID: "sess-1",
			ParentAgentID:   "file-backend",
			Role:            "file-summarizer",
			Prompt:          "p",
			Mode:            ModeAsync,
			MaxRetries:      3,
			OnFail:          OnFailRetry,
		})
		if err != nil {
			t.Errorf("Spawn: %v", err)
		}
		runID <- id
	}()

	id := <-runID
	if id == "" {
		t.Fatal("Spawn returned empty id")
	}
	// Wait for the runner to actually enter Run on the first attempt.
	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not start within 2s")
	}

	if err := svc.Cancel(context.Background(), id); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	// Poll for terminal state. The loop's ctx-cancelled branch in
	// shouldRetry should bail without a second runner invocation.
	deadline := time.Now().Add(2 * time.Second)
	var run *Run
	for time.Now().Before(deadline) {
		r, err := svc.Status(context.Background(), id)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if IsTerminalStatus(r.Status) {
			run = r
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if run == nil {
		t.Fatal("run did not reach a terminal state within 2s")
	}
	if run.Status != StatusCancelled {
		t.Errorf("final Status = %q, want %q", run.Status, StatusCancelled)
	}
	if got := runner.calls.Load(); got != 1 {
		t.Errorf("runner call count = %d, want 1 (cancel must suppress retry)", got)
	}
}

// blockingRunner blocks on its ctx so a test can deterministically Cancel
// mid-flight. Records a started signal so the test knows the runner has
// entered Run before issuing Cancel.
type blockingRunner struct {
	calls   atomic.Int32
	started chan struct{}
}

func (r *blockingRunner) Run(ctx context.Context, _ *Run) (*Result, error) {
	r.calls.Add(1)
	select {
	case r.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestExecute_MaxRetriesZeroIsSingleShot pins that MaxRetries=0 (when
// passed explicitly as the value -1, which normalizes to 0) disables
// retry entirely. A negative MaxRetries is normalized to zero in Spawn.
func TestExecute_MaxRetriesZeroIsSingleShot(t *testing.T) {
	db, _ := newTestDB(t)
	runner := &countingRunner{outcomes: []runOutcome{failOutcome(), failOutcome()}}
	svc := NewService(db, runner, &stubPoster{}, nil, stubSettings{})

	id, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1", ParentAgentID: "p", Role: "r", Prompt: "p", Mode: ModeSync,
		MaxRetries: -1, // normalize to 0 → no retries
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got := runner.callCount(); got != 1 {
		t.Errorf("runner call count = %d, want 1 (MaxRetries=0 single-shot)", got)
	}
	run, _ := svc.Status(context.Background(), id)
	if run.MaxRetries != 0 {
		t.Errorf("MaxRetries persisted = %d, want 0", run.MaxRetries)
	}
}

// TestExecute_DefaultsAreAppliedWhenUnset pins the default-resolution
// behavior at the Spawn boundary: a SpawnRequest that omits MaxRetries
// + OnFail lands on DefaultMaxRetries and DefaultOnFail in the persisted
// row.
func TestExecute_DefaultsAreAppliedWhenUnset(t *testing.T) {
	db, _ := newTestDB(t)
	svc := NewService(db, EchoRunner{}, &stubPoster{}, nil, stubSettings{})

	id, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1", ParentAgentID: "p", Role: "r", Prompt: "p", Mode: ModeSync,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	run, _ := svc.Status(context.Background(), id)
	if run.MaxRetries != DefaultMaxRetries {
		t.Errorf("MaxRetries = %d, want %d", run.MaxRetries, DefaultMaxRetries)
	}
	if run.OnFail != DefaultOnFail {
		t.Errorf("OnFail = %q, want %q", run.OnFail, DefaultOnFail)
	}
	if run.AttemptsJSON != "[]" {
		t.Errorf("AttemptsJSON = %q, want %q", run.AttemptsJSON, "[]")
	}
}

// TestSpawn_RejectsInvalidOnFail pins the validation: an unrecognized
// OnFail value must be rejected before any DB write.
func TestSpawn_RejectsInvalidOnFail(t *testing.T) {
	db, _ := newTestDB(t)
	svc := NewService(db, EchoRunner{}, &stubPoster{}, nil, stubSettings{})
	_, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1", ParentAgentID: "p", Role: "r", Prompt: "p", Mode: ModeSync,
		OnFail: "yolo",
	})
	if err == nil {
		t.Fatal("Spawn with invalid OnFail should return an error")
	}
	if !strings.Contains(err.Error(), "invalid on_fail") {
		t.Errorf("error = %q, want substring %q", err.Error(), "invalid on_fail")
	}
	// No row was inserted.
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM subagent_runs`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 0 {
		t.Errorf("inserted %d rows, want 0 (Spawn must reject before DB write)", count)
	}
}

// TestExecute_StalledRetriesUnderOnFailRetry pins that a stalled outcome
// is retried when on_fail=retry (StatusStalled is retriable per
// IsRetriableStatus).
func TestExecute_StalledRetriesUnderOnFailRetry(t *testing.T) {
	db, _ := newTestDB(t)
	runner := &countingRunner{
		outcomes: []runOutcome{stallOutcome(), stallOutcome(), productiveOutcome("got through")},
	}
	svc := NewService(db, runner, &stubPoster{}, nil, stubSettings{})

	id, _ := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1", ParentAgentID: "p", Role: "r", Prompt: "p", Mode: ModeSync,
		MaxRetries: 3, OnFail: OnFailRetry,
	})
	if got := runner.callCount(); got != 3 {
		t.Errorf("runner call count = %d, want 3", got)
	}
	run, _ := svc.Status(context.Background(), id)
	if run.Status != StatusCompleted {
		t.Errorf("Status = %q, want completed", run.Status)
	}
	if run.RetryCount != 2 {
		t.Errorf("RetryCount = %d, want 2", run.RetryCount)
	}
}

// TestExecute_StalledOnFailBlockSuppressesRetry pins the symmetry:
// on_fail=block suppresses retry for stalled (not just failed).
func TestExecute_StalledOnFailBlockSuppressesRetry(t *testing.T) {
	db, _ := newTestDB(t)
	runner := &countingRunner{outcomes: []runOutcome{stallOutcome(), stallOutcome()}}
	svc := NewService(db, runner, &stubPoster{}, nil, stubSettings{})

	id, _ := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1", ParentAgentID: "p", Role: "r", Prompt: "p", Mode: ModeSync,
		MaxRetries: 3, OnFail: OnFailBlock,
	})
	if got := runner.callCount(); got != 1 {
		t.Errorf("runner call count = %d, want 1 (block must suppress stalled retry)", got)
	}
	run, _ := svc.Status(context.Background(), id)
	if run.Status != StatusStalled {
		t.Errorf("Status = %q, want stalled", run.Status)
	}
}

// TestAttemptsJSON_RoundTrips pins the attempts_json shape — every prior
// attempt's terminal fields must be recoverable verbatim from the blob.
func TestAttemptsJSON_RoundTrips(t *testing.T) {
	prior := "[]"
	updated := appendAttempt(prior, attemptRecord{
		Status:      StatusFailed,
		Error:       "first failure",
		ResultJSON:  `{"partial":true}`,
		StartedAt:   "2026-05-20T00:00:00Z",
		CompletedAt: "2026-05-20T00:00:01Z",
	})
	updated = appendAttempt(updated, attemptRecord{
		Status:      StatusOverBudget,
		Error:       "context canceled",
		ResultJSON:  `{"partial":true,"tools":{"calls":3}}`,
		StartedAt:   "2026-05-20T00:00:02Z",
		CompletedAt: "2026-05-20T00:00:03Z",
	})

	var attempts []attemptRecord
	if err := json.Unmarshal([]byte(updated), &attempts); err != nil {
		t.Fatalf("unmarshal: %v\nblob=%s", err, updated)
	}
	if len(attempts) != 2 {
		t.Fatalf("len = %d, want 2", len(attempts))
	}
	if attempts[0].Status != StatusFailed || attempts[0].Error != "first failure" {
		t.Errorf("attempt[0] = %+v", attempts[0])
	}
	if attempts[1].Status != StatusOverBudget {
		t.Errorf("attempt[1].Status = %q, want over_budget", attempts[1].Status)
	}
	if got := lastAttemptStatus(updated); got != StatusOverBudget {
		t.Errorf("lastAttemptStatus = %q, want over_budget", got)
	}
}

// TestAppendAttempt_MalformedPriorRecoversToSingleEntry pins the safety
// net: a corrupt attempts_json column must not cascade — appendAttempt
// rebuilds from the new entry rather than returning an error.
func TestAppendAttempt_MalformedPriorRecoversToSingleEntry(t *testing.T) {
	out := appendAttempt("{not json", attemptRecord{Status: StatusFailed, Error: "boom"})
	var attempts []attemptRecord
	if err := json.Unmarshal([]byte(out), &attempts); err != nil {
		t.Fatalf("unmarshal recovered blob: %v", err)
	}
	if len(attempts) != 1 || attempts[0].Status != StatusFailed {
		t.Errorf("recovered attempts = %+v, want single failed entry", attempts)
	}
}

// TestIsRetriableStatus_TaxonomyCoverage pins the full retriability map
// against the status taxonomy so a future status addition is forced
// through this gate.
func TestIsRetriableStatus_TaxonomyCoverage(t *testing.T) {
	cases := map[string]bool{
		StatusOverBudget: true,
		StatusStalled:    true,
		StatusFailed:     true,
		StatusCompleted:  false,
		StatusCancelled:  false,
		StatusRejected:   false,
		StatusRequested:  false,
		StatusApproved:   false,
		StatusRunning:    false,
	}
	for s, want := range cases {
		if got := IsRetriableStatus(s); got != want {
			t.Errorf("IsRetriableStatus(%q) = %v, want %v", s, got, want)
		}
	}
}

// TestIsValidOnFail_TaxonomyCoverage pins the on_fail taxonomy.
func TestIsValidOnFail_TaxonomyCoverage(t *testing.T) {
	for _, ok := range []string{OnFailRetry, OnFailBlock, OnFailEscalate} {
		if !IsValidOnFail(ok) {
			t.Errorf("IsValidOnFail(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "yolo", "fail", "ignore", "retry  "} {
		if IsValidOnFail(bad) {
			t.Errorf("IsValidOnFail(%q) = true, want false", bad)
		}
	}
}
