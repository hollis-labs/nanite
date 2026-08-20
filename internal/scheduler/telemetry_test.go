package scheduler

// TASKS/scheduling/06-schedule-fire-telemetry.md's own regression
// requirements: prove each of the three real dispatch outcomes produces a
// correctly-distinguished event_log row at category="schedule_fire", and
// prove the three telemetry streams ("reflex", "selftool_reaction",
// "schedule_fire") stay independently queryable via the existing
// category-filtered event-log read path with no cross-contamination.
//
// All tests here drive telemetry through RetryingRunner.Enqueue (the real
// call site), not by calling EmitScheduleFireTrace directly, except for
// the one dedicated nil-TraceStore unit test -- matching this task's own
// "regression test proves a successful dispatch, a retried-then-succeeded
// dispatch, and an exhausted dispatch..." wording, which is about
// Enqueue's real outcomes, not the telemetry function in isolation.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	gosched "github.com/hollis-labs/go-scheduler"

	"github.com/hollis-labs/nanite/internal/store"
)

// tracingTestRunner mirrors retrying_runner_test.go's own tinyBackoffRunner
// helper (a single-nanosecond backoff window, real clock, for test speed)
// but additionally wires Traces so this file's tests can assert against
// real event_log rows. A separate helper, not a tinyBackoffRunner
// parameter change, to avoid touching that already-reviewed helper's
// signature (and every existing 04 test that calls it) for a need only
// this file's tests have.
func tracingTestRunner(inner gosched.Runner, s *store.Store, disabler ScheduleDisabler) *RetryingRunner {
	return &RetryingRunner{
		Inner:     inner,
		Runs:      s,
		Schedules: s,
		Disabler:  disabler,
		Logger:    testLogger(),
		Traces:    s,
		BaseDelay: time.Nanosecond,
		MaxDelay:  time.Nanosecond,
	}
}

func decodeTraceMetadata(t *testing.T, metadata string) map[string]any {
	t.Helper()
	var rec map[string]any
	if err := json.Unmarshal([]byte(metadata), &rec); err != nil {
		t.Fatalf("unmarshal event_log metadata %q: %v", metadata, err)
	}
	return rec
}

// TestRetryingRunner_ScheduleFireTelemetry_Success proves a successful
// dispatch produces exactly one event_log row at category="schedule_fire",
// outcome="success", with no error field.
func TestRetryingRunner_ScheduleFireTelemetry_Success(t *testing.T) {
	ctx := context.Background()
	s := newAdapterTestStore(t)
	agent := makeAdapterTestAgent(t, s, "telemetry-success")
	schedID := makeRetryTestSchedule(t, s, agent.ID, "sched-telemetry-success", 3, store.ScheduleOnFailNotify)

	inner := &fakeInnerRunner{err: nil}
	runner := tracingTestRunner(inner, s, nil)

	job := gosched.Job{
		ScheduleID: schedID,
		RunID:      "run-success-1",
		JobType:    JobTypeDurableAgentWake,
		Payload:    []byte(`{"instance_id":"inst-1"}`),
		FiredAt:    time.Now(),
	}

	if err := runner.Enqueue(ctx, job); err != nil {
		t.Fatalf("Enqueue: unexpected error: %v", err)
	}

	events, err := s.ListEvents(CategoryScheduleFire, 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("ListEvents(schedule_fire) = %d rows, want 1", len(events))
	}

	ev := events[0]
	if ev.EventType != JobTypeDurableAgentWake {
		t.Fatalf("event_type = %q, want %q", ev.EventType, JobTypeDurableAgentWake)
	}
	if ev.Category != CategoryScheduleFire {
		t.Fatalf("category = %q, want %q", ev.Category, CategoryScheduleFire)
	}
	if ev.Detail != "retry-test-"+schedID {
		t.Fatalf("detail = %q, want schedule name %q", ev.Detail, "retry-test-"+schedID)
	}

	rec := decodeTraceMetadata(t, ev.Metadata)
	if rec["outcome"] != "success" {
		t.Fatalf("metadata.outcome = %v, want %q", rec["outcome"], "success")
	}
	if rec["schedule_id"] != schedID {
		t.Fatalf("metadata.schedule_id = %v, want %q", rec["schedule_id"], schedID)
	}
	if rec["run_id"] != job.RunID {
		t.Fatalf("metadata.run_id = %v, want %q", rec["run_id"], job.RunID)
	}
	if got := rec["attempt_count"]; got != float64(1) {
		t.Fatalf("metadata.attempt_count = %v, want 1", got)
	}
	if _, hasErr := rec["error"]; hasErr {
		t.Fatalf("metadata.error unexpectedly present on a success row: %v", rec["error"])
	}
	if _, hasBK := rec["bookkeeping_error"]; hasBK {
		t.Fatalf("metadata.bookkeeping_error unexpectedly present: %v", rec["bookkeeping_error"])
	}
}

// TestRetryingRunner_ScheduleFireTelemetry_RetryThenSuccess_TwoDistinctRows
// proves a retried-then-succeeded dispatch produces two distinct,
// correctly-outcome-labeled event_log rows correlated to the same
// underlying schedule_runs row (schedule_run_row_id), even though each
// attempt's gosched.Job.RunID differs (matching real go-scheduler
// behavior -- see RetryingRunner's own doc comment on Job.RunID
// instability across retries).
func TestRetryingRunner_ScheduleFireTelemetry_RetryThenSuccess_TwoDistinctRows(t *testing.T) {
	ctx := context.Background()
	s := newAdapterTestStore(t)
	agent := makeAdapterTestAgent(t, s, "telemetry-retry-success")
	schedID := makeRetryTestSchedule(t, s, agent.ID, "sched-telemetry-retry-success", 3, store.ScheduleOnFailNotify)

	inner := &fakeInnerRunner{errs: []error{errors.New("transient dispatch failure")}, err: nil}
	runner := tracingTestRunner(inner, s, nil)

	job1 := gosched.Job{ScheduleID: schedID, RunID: "run-rts-1", JobType: JobTypeCommandRun, Payload: []byte(`{"command":"noop"}`), FiredAt: time.Now()}
	if err := runner.Enqueue(ctx, job1); err == nil {
		t.Fatalf("attempt 1: expected error, got nil")
	}

	job2 := job1
	job2.RunID = "run-rts-2" // fresh RunID per tick, matching real go-scheduler
	if err := runner.Enqueue(ctx, job2); err != nil {
		t.Fatalf("attempt 2: expected nil (success), got %v", err)
	}

	events, err := s.ListEvents(CategoryScheduleFire, 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("ListEvents(schedule_fire) = %d rows, want 2", len(events))
	}

	byOutcome := map[string]map[string]any{}
	for _, ev := range events {
		rec := decodeTraceMetadata(t, ev.Metadata)
		outcome, _ := rec["outcome"].(string)
		byOutcome[outcome] = rec
	}

	retryRec, ok := byOutcome["retry"]
	if !ok {
		t.Fatalf("no retry-outcome row found among rows: %+v", byOutcome)
	}
	successRec, ok := byOutcome["success"]
	if !ok {
		t.Fatalf("no success-outcome row found among rows: %+v", byOutcome)
	}

	if got := retryRec["attempt_count"]; got != float64(1) {
		t.Fatalf("retry row attempt_count = %v, want 1", got)
	}
	if got := successRec["attempt_count"]; got != float64(2) {
		t.Fatalf("success row attempt_count = %v, want 2", got)
	}
	if errStr, _ := retryRec["error"].(string); errStr == "" {
		t.Fatalf("retry row missing metadata.error")
	}
	if _, hasErr := successRec["error"]; hasErr {
		t.Fatalf("success row unexpectedly carries metadata.error: %v", successRec["error"])
	}
	if retryRec["run_id"] == successRec["run_id"] {
		t.Fatalf("retry/success rows unexpectedly share run_id %v (job.RunID should differ per real tick)", retryRec["run_id"])
	}
	if retryRec["schedule_run_row_id"] != successRec["schedule_run_row_id"] {
		t.Fatalf("retry/success rows disagree on schedule_run_row_id: %v vs %v (both attempts belong to the same firing)",
			retryRec["schedule_run_row_id"], successRec["schedule_run_row_id"])
	}
}

// TestRetryingRunner_ScheduleFireTelemetry_Exhausted proves an exhausted
// (on_fail applied) dispatch produces exactly one event_log row with
// outcome="exhausted" and enough detail (error, attempt_count,
// max_retries, on_fail) for an operator to understand why the schedule
// stopped retrying without a separate schedule_runs query.
func TestRetryingRunner_ScheduleFireTelemetry_Exhausted(t *testing.T) {
	ctx := context.Background()
	s := newAdapterTestStore(t)
	agent := makeAdapterTestAgent(t, s, "telemetry-exhausted")
	schedID := makeRetryTestSchedule(t, s, agent.ID, "sched-telemetry-exhausted", 1, store.ScheduleOnFailNotify)

	inner := &fakeInnerRunner{err: errors.New("dispatch failed permanently")}
	runner := tracingTestRunner(inner, s, nil)

	job := gosched.Job{
		ScheduleID: schedID,
		RunID:      "run-exhausted-1",
		JobType:    JobTypeReflexDispatch,
		Payload:    []byte(`{"reflex_id":"rx-1"}`),
		FiredAt:    time.Now(),
	}

	if err := runner.Enqueue(ctx, job); err != nil {
		t.Fatalf("Enqueue: expected nil (exhausted, stop retry loop), got %v", err)
	}

	events, err := s.ListEvents(CategoryScheduleFire, 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("ListEvents(schedule_fire) = %d rows, want 1", len(events))
	}

	rec := decodeTraceMetadata(t, events[0].Metadata)
	if rec["outcome"] != "exhausted" {
		t.Fatalf("metadata.outcome = %v, want %q", rec["outcome"], "exhausted")
	}
	if rec["on_fail"] != store.ScheduleOnFailNotify {
		t.Fatalf("metadata.on_fail = %v, want %q", rec["on_fail"], store.ScheduleOnFailNotify)
	}
	if got := rec["max_retries"]; got != float64(1) {
		t.Fatalf("metadata.max_retries = %v, want 1", got)
	}
	if got := rec["attempt_count"]; got != float64(1) {
		t.Fatalf("metadata.attempt_count = %v, want 1", got)
	}
	if errStr, _ := rec["error"].(string); errStr == "" {
		t.Fatalf("metadata.error missing on exhausted row")
	}
}

// TestRetryingRunner_ScheduleFireTelemetry_BackoffWindowSkip_NoTraceRow
// locks in this task's own documented design call: a short-circuited
// backoff-window skip (RetryingRunner.Enqueue step 2, ErrBackoffActive) is
// NOT a real dispatch attempt and gets no event_log row -- only the one
// real dispatch attempt that preceded it does.
func TestRetryingRunner_ScheduleFireTelemetry_BackoffWindowSkip_NoTraceRow(t *testing.T) {
	ctx := context.Background()
	s := newAdapterTestStore(t)
	agent := makeAdapterTestAgent(t, s, "telemetry-backoff-skip")
	schedID := makeRetryTestSchedule(t, s, agent.ID, "sched-telemetry-backoff-skip", 5, store.ScheduleOnFailNotify)

	inner := &fakeInnerRunner{err: errors.New("dispatch failed")}
	runner := &RetryingRunner{
		Inner:     inner,
		Runs:      s,
		Schedules: s,
		Logger:    testLogger(),
		Traces:    s,
		BaseDelay: time.Hour, // still active on the very next call
		MaxDelay:  time.Hour,
	}

	job := gosched.Job{ScheduleID: schedID, RunID: "run-backoff-1", JobType: JobTypeDurableAgentWake, FiredAt: time.Now()}

	if err := runner.Enqueue(ctx, job); err == nil {
		t.Fatalf("first attempt: expected error, got nil")
	}
	if err := runner.Enqueue(ctx, job); !errors.Is(err, ErrBackoffActive) {
		t.Fatalf("second attempt: got err=%v, want ErrBackoffActive", err)
	}

	events, err := s.ListEvents(CategoryScheduleFire, 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("ListEvents(schedule_fire) = %d rows, want exactly 1 (the real attempt only, not the backoff-window skip)", len(events))
	}
}

// TestRetryingRunner_ScheduleFireTelemetry_DuplicateJob_NoTraceRow proves
// gosched.ErrDuplicateJob's pass-through (no schedule_runs write either)
// produces no event_log row -- consistent with the same "no bookkeeping
// row to correlate a trace row against" reasoning.
func TestRetryingRunner_ScheduleFireTelemetry_DuplicateJob_NoTraceRow(t *testing.T) {
	ctx := context.Background()
	s := newAdapterTestStore(t)
	agent := makeAdapterTestAgent(t, s, "telemetry-dup")
	schedID := makeRetryTestSchedule(t, s, agent.ID, "sched-telemetry-dup", 3, store.ScheduleOnFailNotify)

	dupErr := fmt.Errorf("enqueue run: %w", gosched.ErrDuplicateJob)
	inner := &fakeInnerRunner{err: dupErr}
	runner := tracingTestRunner(inner, s, nil)

	job := gosched.Job{ScheduleID: schedID, RunID: "run-dup-1", JobType: JobTypeDurableAgentWake, FiredAt: time.Now()}
	if err := runner.Enqueue(ctx, job); !errors.Is(err, gosched.ErrDuplicateJob) {
		t.Fatalf("Enqueue: got err=%v, want it to wrap gosched.ErrDuplicateJob", err)
	}

	events, err := s.ListEvents(CategoryScheduleFire, 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("ListEvents(schedule_fire) = %d rows, want 0 (duplicate-job pass-through gets no trace row)", len(events))
	}
}

// TestScheduleFireTelemetry_IndependentlyQueryableAcrossThreeStreams
// proves the three telemetry streams ("reflex", "selftool_reaction",
// "schedule_fire") stay independently queryable via the existing
// category-filtered ListEvents path, with no cross-contamination -- the
// same proof pattern TASKS/harness-reactive-self-tools/
// 05-selftool-reaction-telemetry.md's own cross-stream test already
// established for its two streams, extended here to all three.
func TestScheduleFireTelemetry_IndependentlyQueryableAcrossThreeStreams(t *testing.T) {
	ctx := context.Background()
	s := newAdapterTestStore(t)
	agent := makeAdapterTestAgent(t, s, "telemetry-cross-stream")
	schedID := makeRetryTestSchedule(t, s, agent.ID, "sched-telemetry-cross-stream", 3, store.ScheduleOnFailNotify)

	// Synthesized directly via (*store.Store).LogEvent -- deliberately not
	// importing internal/agent/reflexes or internal/selftools/reactions,
	// which would widen this package's dependency surface for a
	// test-only need (mirroring 05's own documented reasoning for the
	// identical choice).
	s.LogEvent("", "dispatch_to_agent", "reflex", "some-reflex", `{"reflex_id":"rx-1"}`)
	s.LogEvent("", "render_card", "selftool_reaction", "some_tool", `{"tool_name":"some_tool"}`)

	inner := &fakeInnerRunner{err: nil}
	runner := tracingTestRunner(inner, s, nil)
	job := gosched.Job{ScheduleID: schedID, RunID: "run-cross-1", JobType: JobTypeDurableAgentWake, FiredAt: time.Now()}
	if err := runner.Enqueue(ctx, job); err != nil {
		t.Fatalf("Enqueue: unexpected error: %v", err)
	}

	reflexRows, err := s.ListEvents("reflex", 50)
	if err != nil {
		t.Fatalf("ListEvents(reflex): %v", err)
	}
	if len(reflexRows) != 1 {
		t.Fatalf("ListEvents(reflex) = %d rows, want 1", len(reflexRows))
	}

	reactionRows, err := s.ListEvents("selftool_reaction", 50)
	if err != nil {
		t.Fatalf("ListEvents(selftool_reaction): %v", err)
	}
	if len(reactionRows) != 1 {
		t.Fatalf("ListEvents(selftool_reaction) = %d rows, want 1", len(reactionRows))
	}

	scheduleRows, err := s.ListEvents(CategoryScheduleFire, 50)
	if err != nil {
		t.Fatalf("ListEvents(schedule_fire): %v", err)
	}
	if len(scheduleRows) != 1 {
		t.Fatalf("ListEvents(schedule_fire) = %d rows, want 1", len(scheduleRows))
	}

	all, err := s.ListEvents("", 50)
	if err != nil {
		t.Fatalf(`ListEvents(""): %v`, err)
	}
	if len(all) != 3 {
		t.Fatalf(`ListEvents("") = %d rows, want 3 (no cross-contamination)`, len(all))
	}
}

// TestEmitScheduleFireTrace_NilTraceStore_NoOp proves EmitScheduleFireTrace
// tolerates a nil TraceStore (RetryingRunner.Traces left unconfigured) as
// a safe no-op, not a caller-programming error -- see this file's Work
// Log for why that's the deliberate design call here, unlike
// internal/selftools/reactions.EmitReactionTrace's nil-ts-is-an-error
// convention.
func TestEmitScheduleFireTrace_NilTraceStore_NoOp(t *testing.T) {
	EmitScheduleFireTrace(context.Background(), nil, nil, ScheduleFireTraceInput{
		Job:     gosched.Job{ScheduleID: "sched-x", RunID: "run-x", JobType: JobTypeCommandRun},
		Outcome: ScheduleFireOutcomeSuccess,
	})
}
