package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	gosched "github.com/hollis-labs/go-scheduler"

	"github.com/hollis-labs/nanite/internal/store"
)

// --- test helpers -----------------------------------------------------

// fakeInnerRunner is a gosched.Runner that counts calls and returns
// whatever the test configures (a fixed error, or a per-call sequence).
type fakeInnerRunner struct {
	mu    sync.Mutex
	calls int
	err   error // returned on every call unless errs is set
	errs  []error
}

func (f *fakeInnerRunner) Enqueue(ctx context.Context, job gosched.Job) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	idx := f.calls
	f.calls++
	if idx < len(f.errs) {
		return f.errs[idx]
	}
	return f.err
}

func (f *fakeInnerRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeDisabler counts DisableSchedule calls.
type fakeDisabler struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeDisabler) DisableSchedule(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, id)
	return nil
}

func (f *fakeDisabler) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(nopWriter{}, nil))
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

// makeRetryTestSchedule inserts an agent_schedules row with the given
// retry policy and returns its ID.
func makeRetryTestSchedule(t *testing.T, s *store.Store, agentID, id string, maxRetries int64, onFail string) string {
	t.Helper()
	if err := s.InsertAgentSchedule(context.Background(), store.AgentSchedule{
		ID:           id,
		AgentID:      agentID,
		Name:         "retry-test-" + id,
		ScheduleKind: store.ScheduleKindCron,
		ScheduleSpec: "0 9 * * *",
		Body:         "test body",
		MaxRetries:   maxRetries,
		OnFail:       onFail,
	}); err != nil {
		t.Fatalf("InsertAgentSchedule: %v", err)
	}
	return id
}

// tinyBackoffRunner builds a RetryingRunner whose backoff window is a
// single nanosecond -- small enough that real wall-clock progression
// between two sequential Go statements in a test (which takes comfortably
// more than 1ns: DB round trips, mutex locks, function-call overhead) has
// already cleared it by the time the test issues the next Enqueue call.
// This collapses the backoff window for test speed using the real clock
// (r.Now left nil), rather than a fake clock -- see this task's Work Log
// for why this approach was chosen over injecting a fake time source.
func tinyBackoffRunner(inner gosched.Runner, s *store.Store, disabler ScheduleDisabler) *RetryingRunner {
	return &RetryingRunner{
		Inner:     inner,
		Runs:      s,
		Schedules: s,
		Disabler:  disabler,
		Logger:    testLogger(),
		BaseDelay: time.Nanosecond,
		MaxDelay:  time.Nanosecond,
	}
}

// --- tests --------------------------------------------------------------

// TestRetryingRunner_MaxRetries_ExactlyThreeRealAttempts_ThenDisables
// proves: max_retries=3, inner Runner always fails -> exactly 3 real
// dispatch attempts (not 4, not 1), then on_fail=disable applies exactly
// once, and a 4th Enqueue call (simulating go-scheduler continuing to
// tick with an unchanged Job after this layer already concluded the
// firing) produces no further real dispatch attempt.
func TestRetryingRunner_MaxRetries_ExactlyThreeRealAttempts_ThenDisables(t *testing.T) {
	s := newAdapterTestStore(t)
	agent := makeAdapterTestAgent(t, s, "retry-exhaust")
	schedID := makeRetryTestSchedule(t, s, agent.ID, "sched-exhaust", 3, store.ScheduleOnFailDisable)

	inner := &fakeInnerRunner{err: errors.New("dispatch failed")}
	disabler := &fakeDisabler{}
	runner := tinyBackoffRunner(inner, s, disabler)

	job := gosched.Job{ScheduleID: schedID, RunID: "run-fixed", JobType: "durable_agent_wake", FiredAt: time.Now()}

	// Attempts 1 and 2: still within budget, original error returned.
	for i := 0; i < 2; i++ {
		err := runner.Enqueue(context.Background(), job)
		if err == nil {
			t.Fatalf("attempt %d: expected error, got nil", i+1)
		}
		if errors.Is(err, ErrBackoffActive) || errors.Is(err, ErrRunTerminal) {
			t.Fatalf("attempt %d: unexpected sentinel error: %v", i+1, err)
		}
	}
	if got := inner.callCount(); got != 2 {
		t.Fatalf("after 2 attempts: inner call count = %d, want 2", got)
	}

	// Attempt 3: exhausts the budget -- on_fail applies, nil returned to
	// stop go-scheduler's own retry loop.
	if err := runner.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("attempt 3 (exhausting): expected nil, got %v", err)
	}
	if got := inner.callCount(); got != 3 {
		t.Fatalf("after 3 attempts: inner call count = %d, want 3", got)
	}
	if got := disabler.callCount(); got != 1 {
		t.Fatalf("disabler call count = %d, want exactly 1", got)
	}

	// 4th Enqueue call with the SAME job (simulating continued ticking):
	// no further real dispatch attempt.
	err := runner.Enqueue(context.Background(), job)
	if err == nil {
		t.Fatalf("attempt 4: expected an error (terminal firing), got nil")
	}
	if !errors.Is(err, ErrRunTerminal) {
		t.Fatalf("attempt 4: got err=%v, want ErrRunTerminal", err)
	}
	if got := inner.callCount(); got != 3 {
		t.Fatalf("after attempt 4: inner call count = %d, want still 3 (no further real attempt)", got)
	}
	if got := disabler.callCount(); got != 1 {
		t.Fatalf("after attempt 4: disabler call count = %d, want still 1 (not disabled twice)", got)
	}

	row, err := s.GetLatestScheduleRun(context.Background(), schedID)
	if err != nil {
		t.Fatalf("GetLatestScheduleRun: %v", err)
	}
	if row.Status != store.ScheduleRunStatusExhausted || row.AttemptCount != 3 {
		t.Fatalf("unexpected final schedule_runs row: %+v", row)
	}
}

// TestRetryingRunner_OnFailNotify_DoesNotDisable and its 'retry' sibling
// prove on_fail=notify (and on_fail=retry, per task 01's resolution) does
// NOT call DisableSchedule at exhaustion.
func TestRetryingRunner_OnFailNotify_DoesNotDisable(t *testing.T) {
	for _, onFail := range []string{store.ScheduleOnFailNotify, store.ScheduleOnFailRetry} {
		t.Run(onFail, func(t *testing.T) {
			s := newAdapterTestStore(t)
			agent := makeAdapterTestAgent(t, s, "retry-notify-"+onFail)
			schedID := makeRetryTestSchedule(t, s, agent.ID, "sched-notify-"+onFail, 1, onFail)

			inner := &fakeInnerRunner{err: errors.New("dispatch failed")}
			disabler := &fakeDisabler{}
			runner := tinyBackoffRunner(inner, s, disabler)

			job := gosched.Job{ScheduleID: schedID, RunID: "run-fixed", JobType: "durable_agent_wake", FiredAt: time.Now()}

			// max_retries=1: the very first attempt exhausts immediately.
			if err := runner.Enqueue(context.Background(), job); err != nil {
				t.Fatalf("Enqueue: expected nil (exhausted, on_fail=%s), got %v", onFail, err)
			}
			if got := inner.callCount(); got != 1 {
				t.Fatalf("inner call count = %d, want 1", got)
			}
			if got := disabler.callCount(); got != 0 {
				t.Fatalf("disabler call count = %d, want 0 (on_fail=%s must not disable)", got, onFail)
			}

			row, err := s.GetLatestScheduleRun(context.Background(), schedID)
			if err != nil {
				t.Fatalf("GetLatestScheduleRun: %v", err)
			}
			if row.Status != store.ScheduleRunStatusExhausted {
				t.Fatalf("row status = %q, want exhausted", row.Status)
			}
		})
	}
}

// TestRetryingRunner_BackoffWindow_ShortCircuitsWithoutInnerCall proves a
// between-attempts Enqueue call inside the backoff window returns an error
// without invoking the inner Runner.
func TestRetryingRunner_BackoffWindow_ShortCircuitsWithoutInnerCall(t *testing.T) {
	s := newAdapterTestStore(t)
	agent := makeAdapterTestAgent(t, s, "retry-backoff")
	schedID := makeRetryTestSchedule(t, s, agent.ID, "sched-backoff", 5, store.ScheduleOnFailNotify)

	inner := &fakeInnerRunner{err: errors.New("dispatch failed")}
	runner := &RetryingRunner{
		Inner:     inner,
		Runs:      s,
		Schedules: s,
		Logger:    testLogger(),
		BaseDelay: time.Hour, // large enough that the window is still active on the very next call
		MaxDelay:  time.Hour,
	}

	job := gosched.Job{ScheduleID: schedID, RunID: "run-fixed", JobType: "durable_agent_wake", FiredAt: time.Now()}

	if err := runner.Enqueue(context.Background(), job); err == nil {
		t.Fatalf("first attempt: expected error, got nil")
	}
	if got := inner.callCount(); got != 1 {
		t.Fatalf("after first attempt: inner call count = %d, want 1", got)
	}

	// Second call, immediately: inside the backoff window.
	err := runner.Enqueue(context.Background(), job)
	if !errors.Is(err, ErrBackoffActive) {
		t.Fatalf("second attempt: got err=%v, want ErrBackoffActive", err)
	}
	if got := inner.callCount(); got != 1 {
		t.Fatalf("after second attempt: inner call count = %d, want still 1 (cheap short-circuit)", got)
	}
}

// TestRetryingRunner_DuplicateJobPassesThroughUnaffected proves
// gosched.ErrDuplicateJob passes through unaffected by retry bookkeeping:
// no attempt_count bump, no status change.
func TestRetryingRunner_DuplicateJobPassesThroughUnaffected(t *testing.T) {
	s := newAdapterTestStore(t)
	agent := makeAdapterTestAgent(t, s, "retry-dup")
	schedID := makeRetryTestSchedule(t, s, agent.ID, "sched-dup", 3, store.ScheduleOnFailNotify)

	dupErr := fmt.Errorf("enqueue run: %w", gosched.ErrDuplicateJob)
	inner := &fakeInnerRunner{err: dupErr}
	runner := tinyBackoffRunner(inner, s, nil)

	job := gosched.Job{ScheduleID: schedID, RunID: "run-fixed", JobType: "durable_agent_wake", FiredAt: time.Now()}

	err := runner.Enqueue(context.Background(), job)
	if !errors.Is(err, gosched.ErrDuplicateJob) {
		t.Fatalf("Enqueue: got err=%v, want it to wrap gosched.ErrDuplicateJob", err)
	}

	row, gerr := s.GetOpenScheduleRun(context.Background(), schedID)
	if gerr != nil {
		t.Fatalf("GetOpenScheduleRun: %v", gerr)
	}
	if row.AttemptCount != 0 {
		t.Fatalf("AttemptCount = %d, want 0 (duplicate must not consume a retry attempt)", row.AttemptCount)
	}
	if row.Status != store.ScheduleRunStatusPending {
		t.Fatalf("Status = %q, want pending (unchanged)", row.Status)
	}
}

// TestRetryingRunner_Success_MarksRowSucceeded proves a successful dispatch
// marks the schedule_runs row succeeded and leaves no open row behind.
func TestRetryingRunner_Success_MarksRowSucceeded(t *testing.T) {
	s := newAdapterTestStore(t)
	agent := makeAdapterTestAgent(t, s, "retry-success")
	schedID := makeRetryTestSchedule(t, s, agent.ID, "sched-success", 3, store.ScheduleOnFailNotify)

	inner := &fakeInnerRunner{err: nil}
	runner := tinyBackoffRunner(inner, s, nil)

	job := gosched.Job{ScheduleID: schedID, RunID: "run-fixed", JobType: "durable_agent_wake", FiredAt: time.Now()}

	if err := runner.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("Enqueue: unexpected error: %v", err)
	}
	if got := inner.callCount(); got != 1 {
		t.Fatalf("inner call count = %d, want 1", got)
	}

	if _, err := s.GetOpenScheduleRun(context.Background(), schedID); !errors.Is(err, store.ErrScheduleRunNotFound) {
		t.Fatalf("GetOpenScheduleRun after success: got err=%v, want ErrScheduleRunNotFound (succeeded is terminal)", err)
	}
	row, err := s.GetLatestScheduleRun(context.Background(), schedID)
	if err != nil {
		t.Fatalf("GetLatestScheduleRun: %v", err)
	}
	if row.Status != store.ScheduleRunStatusSucceeded || row.AttemptCount != 1 {
		t.Fatalf("unexpected row: %+v", row)
	}
}

// TestRetryingRunner_RetriesAcrossVaryingRunID_StillCountsAsSameFiring is
// the regression proof for this task's core deviation-with-correction: in
// real go-scheduler usage, Job.RunID is regenerated every tick
// (libs/go-scheduler/engine.go's tick(), fmt.Sprintf("sched-%s-%d",
// sch.ID, now.Unix())) -- it is NOT the same value across retry attempts
// of one logical firing. This test simulates that real behavior (a
// different RunID on each Enqueue call for the same ScheduleID) and proves
// retry bookkeeping still correctly accumulates against a single
// schedule_runs row keyed by ScheduleID, not by the (changing) RunID.
func TestRetryingRunner_RetriesAcrossVaryingRunID_StillCountsAsSameFiring(t *testing.T) {
	s := newAdapterTestStore(t)
	agent := makeAdapterTestAgent(t, s, "retry-varying-runid")
	schedID := makeRetryTestSchedule(t, s, agent.ID, "sched-varying-runid", 2, store.ScheduleOnFailNotify)

	inner := &fakeInnerRunner{err: errors.New("dispatch failed")}
	runner := tinyBackoffRunner(inner, s, nil)

	job1 := gosched.Job{ScheduleID: schedID, RunID: "sched-x-1000", JobType: "durable_agent_wake", FiredAt: time.Now()}
	job2 := gosched.Job{ScheduleID: schedID, RunID: "sched-x-1001", JobType: "durable_agent_wake", FiredAt: time.Now()}

	if err := runner.Enqueue(context.Background(), job1); err == nil {
		t.Fatalf("attempt 1 (RunID=%s): expected error, got nil", job1.RunID)
	}
	// attempt 2 exhausts max_retries=2, using a DIFFERENT RunID -- proving
	// this is still recognized as the same firing's second attempt.
	if err := runner.Enqueue(context.Background(), job2); err != nil {
		t.Fatalf("attempt 2 (RunID=%s, exhausting): expected nil, got %v", job2.RunID, err)
	}
	if got := inner.callCount(); got != 2 {
		t.Fatalf("inner call count = %d, want 2", got)
	}

	row, err := s.GetLatestScheduleRun(context.Background(), schedID)
	if err != nil {
		t.Fatalf("GetLatestScheduleRun: %v", err)
	}
	if row.Status != store.ScheduleRunStatusExhausted || row.AttemptCount != 2 {
		t.Fatalf("unexpected row: %+v", row)
	}
	// Only one schedule_runs row was ever created for this schedule --
	// its RunID is job1's (the first attempt's), never updated to job2's.
	if row.RunID != job1.RunID {
		t.Fatalf("RunID = %q, want %q (first attempt's RunID, unchanged across retries)", row.RunID, job1.RunID)
	}
}

// TestRetryingRunner_NewFiringAfterExhaustion_GetsFreshBudget proves a
// genuinely new firing (a different RunID, arriving after the previous
// firing already reached a terminal state) is NOT conflated with the
// concluded firing -- it gets a brand-new schedule_runs row and a fresh
// retry budget, matching real production behavior where go-scheduler only
// re-dispatches a schedule at its real next cron occurrence once this
// layer has returned nil.
func TestRetryingRunner_NewFiringAfterExhaustion_GetsFreshBudget(t *testing.T) {
	s := newAdapterTestStore(t)
	agent := makeAdapterTestAgent(t, s, "retry-new-firing")
	schedID := makeRetryTestSchedule(t, s, agent.ID, "sched-new-firing", 1, store.ScheduleOnFailNotify)

	inner := &fakeInnerRunner{err: errors.New("dispatch failed")}
	runner := tinyBackoffRunner(inner, s, nil)

	job1 := gosched.Job{ScheduleID: schedID, RunID: "firing-1", JobType: "durable_agent_wake", FiredAt: time.Now()}
	if err := runner.Enqueue(context.Background(), job1); err != nil {
		t.Fatalf("firing 1 (max_retries=1, exhausts immediately): expected nil, got %v", err)
	}
	if got := inner.callCount(); got != 1 {
		t.Fatalf("after firing 1: inner call count = %d, want 1", got)
	}

	job2 := gosched.Job{ScheduleID: schedID, RunID: "firing-2", JobType: "durable_agent_wake", FiredAt: time.Now()}
	if err := runner.Enqueue(context.Background(), job2); err != nil {
		t.Fatalf("firing 2 (fresh firing, new RunID): expected nil, got %v", err)
	}
	if got := inner.callCount(); got != 2 {
		t.Fatalf("after firing 2: inner call count = %d, want 2 (a genuinely new firing gets its own real attempt)", got)
	}
}
