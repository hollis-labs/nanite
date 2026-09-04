package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	gosched "github.com/hollis-labs/go-scheduler"

	"github.com/hollis-labs/nanite/internal/store"
)

type schedulerTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *schedulerTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *schedulerTestClock) Set(now time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

func (*schedulerTestClock) NewTicker(time.Duration) gosched.Ticker {
	return inertSchedulerTicker{ch: make(chan time.Time)}
}

type inertSchedulerTicker struct{ ch chan time.Time }

func (t inertSchedulerTicker) C() <-chan time.Time { return t.ch }
func (inertSchedulerTicker) Stop()                 {}

type sequenceRunner struct {
	mu     sync.Mutex
	errs   []error
	cancel context.CancelFunc
	jobs   []gosched.Job
}

func (r *sequenceRunner) Enqueue(_ context.Context, job gosched.Job) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobs = append(r.jobs, job)
	if r.cancel != nil {
		r.cancel()
	}
	if len(r.errs) == 0 {
		return nil
	}
	err := r.errs[0]
	r.errs = r.errs[1:]
	return err
}

func (r *sequenceRunner) snapshot() []gosched.Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]gosched.Job(nil), r.jobs...)
}

func insertEngineSchedule(t *testing.T, s *store.Store, id string, at time.Time, attempts int64, onFail string) {
	t.Helper()
	agent := makeAdapterTestAgent(t, s, "engine-"+id)
	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: id, AgentID: agent.ID, Name: "schedule-" + id,
		ScheduleKind: store.ScheduleKindCron, ScheduleSpec: "* * * * *", Body: "body",
		NextRun: at.Format(time.RFC3339Nano), MaxRetries: attempts, OnFail: onFail,
		JobType: store.ScheduleJobTypeCommandRun, JobPayload: `{"command":"noop"}`,
	})
}

func newObservedEngine(s *store.Store, runner gosched.Runner, clock gosched.Clock) *gosched.Engine {
	adapter := &StoreAdapter{Store: s}
	observer := &PolicyObserver{Schedules: s, Fires: s, Disabler: adapter, Traces: s}
	return gosched.New(adapter, runner, gosched.WithClock(clock), gosched.WithObserver(observer))
}

func decodeTraceMetadata(t *testing.T, metadata string) traceRecord {
	t.Helper()
	var record traceRecord
	if err := json.Unmarshal([]byte(metadata), &record); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	return record
}

func TestEngineRetryBackoffSuccessUsesOneDurableIdentityAndNoDuplicateAccounting(t *testing.T) {
	s := newAdapterTestStore(t)
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	insertEngineSchedule(t, s, "retry-success", now, 3, store.ScheduleOnFailNotify)
	clock := &schedulerTestClock{now: now}
	runner := &sequenceRunner{errs: []error{errors.New("transient"), nil}}
	engine := newObservedEngine(s, runner, clock)

	if err := engine.TickNow(context.Background()); err != nil {
		t.Fatalf("first TickNow: %v", err)
	}
	jobs := runner.snapshot()
	if len(jobs) != 1 || jobs[0].Attempt != 1 || jobs[0].FireID == "" || jobs[0].RunID != jobs[0].FireID {
		t.Fatalf("first jobs = %+v", jobs)
	}
	fire, err := s.GetScheduleFire(context.Background(), jobs[0].FireID)
	if err != nil {
		t.Fatalf("GetScheduleFire: %v", err)
	}
	if fire.Status != store.ScheduleFireStatusRetrying || fire.AttemptCount != 1 {
		t.Fatalf("after first failure: %+v", fire)
	}
	wantRetry := now.Add(scheduleRetryInitialDelay).Format(time.RFC3339Nano)
	if fire.NextAttemptAt != wantRetry {
		t.Fatalf("next_attempt_at = %q, want %q", fire.NextAttemptAt, wantRetry)
	}

	clock.Set(now.Add(scheduleRetryInitialDelay - time.Second))
	if tickErr := engine.TickNow(context.Background()); tickErr != nil {
		t.Fatalf("early TickNow: %v", tickErr)
	}
	if len(runner.snapshot()) != 1 {
		t.Fatal("backoff window dispatched early")
	}
	clock.Set(now.Add(scheduleRetryInitialDelay))
	if tickErr := engine.TickNow(context.Background()); tickErr != nil {
		t.Fatalf("retry TickNow: %v", tickErr)
	}
	jobs = runner.snapshot()
	if len(jobs) != 2 || jobs[1].Attempt != 2 || jobs[1].FireID != jobs[0].FireID {
		t.Fatalf("retry jobs = %+v", jobs)
	}
	fire, _ = s.GetScheduleFire(context.Background(), jobs[0].FireID)
	if fire.Status != store.ScheduleFireStatusSucceeded || fire.AttemptCount != 2 {
		t.Fatalf("terminal fire = %+v", fire)
	}
	events, err := s.ListEvents(context.Background(), CategoryScheduleFire, 10)
	if err != nil || len(events) != 2 {
		t.Fatalf("schedule events = (%+v, %v), want 2", events, err)
	}
	seen := map[string]traceRecord{}
	for _, event := range events {
		record := decodeTraceMetadata(t, event.Metadata)
		seen[record.Outcome] = record
	}
	if seen["retry"].RunID != jobs[0].FireID || seen["success"].RunID != jobs[0].FireID ||
		seen["retry"].ScheduleRunRowID != seen["success"].ScheduleRunRowID ||
		seen["retry"].AttemptCount != 1 || seen["success"].AttemptCount != 2 {
		t.Fatalf("trace correlation = %+v", seen)
	}
}

func TestEngineExhaustionAppliesNaniteDisablePolicyAfterDurableTransition(t *testing.T) {
	s := newAdapterTestStore(t)
	now := time.Date(2026, 9, 4, 13, 0, 0, 0, time.UTC)
	insertEngineSchedule(t, s, "disable-on-fail", now, 1, store.ScheduleOnFailDisable)
	runner := &sequenceRunner{errs: []error{errors.New("permanent")}}
	engine := newObservedEngine(s, runner, &schedulerTestClock{now: now})
	if err := engine.TickNow(context.Background()); err != nil {
		t.Fatalf("TickNow: %v", err)
	}
	jobs := runner.snapshot()
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	fire, _ := s.GetScheduleFire(context.Background(), jobs[0].FireID)
	if fire.Status != store.ScheduleFireStatusExhausted || fire.AttemptCount != 1 {
		t.Fatalf("exhausted fire = %+v", fire)
	}
	schedule, _ := s.GetAgentSchedule(context.Background(), "disable-on-fail")
	if schedule.Status != store.ScheduleStatusExpired {
		t.Fatalf("schedule status = %q, want expired", schedule.Status)
	}
	events, _ := s.ListEvents(context.Background(), CategoryScheduleFire, 10)
	if len(events) != 1 || decodeTraceMetadata(t, events[0].Metadata).Outcome != "exhausted" {
		t.Fatalf("events = %+v", events)
	}
}

func TestEnginePersistsSuccessAfterCallerCancellation(t *testing.T) {
	s := newAdapterTestStore(t)
	now := time.Date(2026, 9, 4, 14, 0, 0, 0, time.UTC)
	insertEngineSchedule(t, s, "cancel-persist", now, 2, store.ScheduleOnFailNotify)
	ctx, cancel := context.WithCancel(context.Background())
	runner := &sequenceRunner{cancel: cancel}
	engine := newObservedEngine(s, runner, &schedulerTestClock{now: now})
	if err := engine.TickNow(ctx); err != nil {
		t.Fatalf("TickNow: %v", err)
	}
	jobs := runner.snapshot()
	fire, err := s.GetScheduleFire(context.Background(), jobs[0].FireID)
	if err != nil || fire.Status != store.ScheduleFireStatusSucceeded {
		t.Fatalf("fire after cancellation = (%+v, %v)", fire, err)
	}
}

func TestEngineRestartRecoversExpiredClaimWithoutConsumingAnotherAttempt(t *testing.T) {
	s := newAdapterTestStore(t)
	now := time.Date(2026, 9, 4, 14, 30, 0, 0, time.UTC)
	insertEngineSchedule(t, s, "restart", now, 2, store.ScheduleOnFailNotify)
	adapter := &StoreAdapter{Store: s}
	schedules, err := adapter.ListDueSchedules(context.Background(), now, 1)
	if err != nil || len(schedules) != 1 {
		t.Fatalf("ListDueSchedules = (%+v, %v)", schedules, err)
	}
	schedule := schedules[0]
	fireID := gosched.DeriveFireID(schedule.ID, schedule.NextRun)
	created, err := adapter.CreateFire(context.Background(), gosched.FireCreation{
		ScheduleID: schedule.ID, ExpectedNext: schedule.NextRun, NextRun: now.Add(time.Hour),
		Fire: gosched.Fire{
			ID: fireID, ScheduleID: schedule.ID, ScheduledAt: schedule.NextRun,
			Status: gosched.FirePending, NextAttemptAt: schedule.NextRun,
			Retry: schedule.Retry, JobType: schedule.JobType, Payload: schedule.Payload,
		},
	})
	if err != nil || !created {
		t.Fatalf("CreateFire = (%v, %v)", created, err)
	}
	firstClaimAt := now.Add(time.Second)
	claimed, won, err := adapter.ClaimFire(context.Background(), gosched.FireClaim{
		FireID: fireID, ExpectedStatus: gosched.FirePending, ExpectedAttempt: 0,
		ClaimedAt: firstClaimAt, ClaimExpiresAt: firstClaimAt.Add(time.Minute),
	})
	if err != nil || !won || claimed.Attempt != 1 {
		t.Fatalf("ClaimFire = (%+v, %v, %v)", claimed, won, err)
	}

	runner := &sequenceRunner{}
	clock := &schedulerTestClock{now: firstClaimAt.Add(30 * time.Second)}
	restarted := newObservedEngine(s, runner, clock)
	if err := restarted.TickNow(context.Background()); err != nil {
		t.Fatalf("pre-expiry TickNow: %v", err)
	}
	if len(runner.snapshot()) != 0 {
		t.Fatal("unexpired claim was redelivered")
	}
	clock.Set(firstClaimAt.Add(time.Minute))
	if err := restarted.TickNow(context.Background()); err != nil {
		t.Fatalf("recovery TickNow: %v", err)
	}
	jobs := runner.snapshot()
	if len(jobs) != 1 || jobs[0].FireID != fireID || jobs[0].Attempt != 1 {
		t.Fatalf("recovered jobs = %+v", jobs)
	}
	fire, _ := s.GetScheduleFire(context.Background(), fireID)
	if fire.Status != store.ScheduleFireStatusSucceeded || fire.AttemptCount != 1 {
		t.Fatalf("recovered fire = %+v", fire)
	}
}

func TestRecoveredAlreadyAcceptedDispatchBecomesSkippedWithoutRepeatingSideEffect(t *testing.T) {
	s := newAdapterTestStore(t)
	now := time.Date(2026, 9, 4, 14, 45, 0, 0, time.UTC)
	insertEngineSchedule(t, s, "accepted-recovery", now, 2, store.ScheduleOnFailNotify)
	adapter := &StoreAdapter{Store: s}
	schedules, err := adapter.ListDueSchedules(context.Background(), now, 1)
	if err != nil || len(schedules) != 1 {
		t.Fatalf("ListDueSchedules = (%+v, %v)", schedules, err)
	}
	schedule := schedules[0]
	fireID := gosched.DeriveFireID(schedule.ID, schedule.NextRun)
	created, err := adapter.CreateFire(context.Background(), gosched.FireCreation{
		ScheduleID: schedule.ID, ExpectedNext: schedule.NextRun, NextRun: now.Add(time.Hour),
		Fire: gosched.Fire{
			ID: fireID, ScheduleID: schedule.ID, ScheduledAt: schedule.NextRun,
			Status: gosched.FirePending, NextAttemptAt: schedule.NextRun,
			Retry: schedule.Retry, JobType: schedule.JobType, Payload: schedule.Payload,
		},
	})
	if err != nil || !created {
		t.Fatalf("CreateFire = (%v, %v)", created, err)
	}
	claimedAt := now.Add(time.Second)
	claimed, won, err := adapter.ClaimFire(context.Background(), gosched.FireClaim{
		FireID: fireID, ExpectedStatus: gosched.FirePending, ExpectedAttempt: 0,
		ClaimedAt: claimedAt, ClaimExpiresAt: claimedAt.Add(time.Minute),
	})
	if err != nil || !won {
		t.Fatalf("ClaimFire = (%+v, %v, %v)", claimed, won, err)
	}

	executor := &fakeCommandExecutor{}
	runner := &RunnerAdapter{Commands: executor, Dispatches: s}
	job := gosched.Job{
		ScheduleID: claimed.ScheduleID, FireID: claimed.ID, RunID: claimed.ID,
		JobType: claimed.JobType, Payload: claimed.Payload,
		ScheduledAt: claimed.ScheduledAt, FiredAt: claimed.FiredAt, Attempt: claimed.Attempt,
	}
	if err := runner.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("initial Enqueue: %v", err)
	}
	if executor.calls != 1 {
		t.Fatalf("initial side-effect calls = %d, want 1", executor.calls)
	}
	persisted, _ := s.GetScheduleFire(context.Background(), fireID)
	if persisted.Status != store.ScheduleFireStatusClaimed || persisted.DispatchAcceptedAt == "" {
		t.Fatalf("accepted pre-crash fire = %+v", persisted)
	}

	clock := &schedulerTestClock{now: claimedAt.Add(time.Minute)}
	restarted := newObservedEngine(s, runner, clock)
	if err := restarted.TickNow(context.Background()); err != nil {
		t.Fatalf("recovery TickNow: %v", err)
	}
	if executor.calls != 1 {
		t.Fatalf("recovery repeated side effect: calls = %d", executor.calls)
	}
	persisted, _ = s.GetScheduleFire(context.Background(), fireID)
	if persisted.Status != store.ScheduleFireStatusSkipped || persisted.AttemptCount != 1 {
		t.Fatalf("recovered accepted fire = %+v", persisted)
	}
	events, _ := s.ListEvents(context.Background(), CategoryScheduleFire, 10)
	if len(events) != 1 || decodeTraceMetadata(t, events[0].Metadata).Outcome != "skipped" {
		t.Fatalf("skip observations = %+v", events)
	}
}

func TestConcurrentEnginesProduceOneTerminalObservation(t *testing.T) {
	s := newAdapterTestStore(t)
	now := time.Date(2026, 9, 4, 15, 0, 0, 0, time.UTC)
	insertEngineSchedule(t, s, "race", now, 2, store.ScheduleOnFailNotify)
	runner := &sequenceRunner{}
	clock := &schedulerTestClock{now: now}
	engines := []*gosched.Engine{newObservedEngine(s, runner, clock), newObservedEngine(s, runner, clock)}
	var wg sync.WaitGroup
	for _, engine := range engines {
		wg.Add(1)
		go func(engine *gosched.Engine) {
			defer wg.Done()
			_ = engine.TickNow(context.Background())
		}(engine)
	}
	wg.Wait()
	if jobs := runner.snapshot(); len(jobs) != 1 {
		t.Fatalf("dispatches = %d, want 1: %+v", len(jobs), jobs)
	}
	events, err := s.ListEvents(context.Background(), CategoryScheduleFire, 10)
	if err != nil || len(events) != 1 || decodeTraceMetadata(t, events[0].Metadata).Outcome != "success" {
		t.Fatalf("terminal observations = (%+v, %v), want exactly one success", events, err)
	}
}
