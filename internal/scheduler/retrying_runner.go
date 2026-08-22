// This file (retrying_runner.go) implements the retry/backoff/on_fail
// policy layer TASKS/scheduling/04-retry-backoff-on-fail-policy.md and
// docs/engineering/architecture/12-scheduling.md's "Retry, backoff, and
// on_fail policy" section describe: go-scheduler itself only offers "retry
// the same firing every second, forever, uncounted" (confirmed directly by
// reading libs/go-scheduler/engine.go's tick() -- a failed Enqueue rolls
// agent_schedules.next_run back to the pre-claim value via
// SetScheduleNextRun, so the same firing becomes due again on the very
// next 1-second tick, with no cap and no backoff), so real retry/backoff/
// failure policy has to live entirely in this Runner-decorator layer.
//
// Decorator-vs-inline call (task step 1): decorator, matching the task
// file's own recommendation. RetryingRunner implements gosched.Runner
// itself and wraps an inner gosched.Runner (03's RunnerAdapter in
// production) -- this keeps 03's per-job-type dispatch logic and this
// file's retry/backoff/schedule_runs bookkeeping independently testable
// (03's own tests already exercise RunnerAdapter directly against fake
// gosched.Job values with zero knowledge of retry policy; this file's own
// tests wrap a fake inner Runner with zero knowledge of job-type
// decoding). Exported (RetryingRunner, not the task's own lowercase
// illustrative retryingRunner) for consistency with this package's two
// existing adapters (StoreAdapter, RunnerAdapter are both exported) and
// because 05-engine-wiring-and-full-replace.md needs to construct and wire
// this type from outside this package (internal/service/container.go).
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	gosched "github.com/hollis-labs/go-scheduler"

	"github.com/hollis-labs/nanite/internal/store"
)

// ErrBackoffActive is returned by RetryingRunner.Enqueue when a firing's
// backoff window (schedule_runs.next_attempt_at) hasn't elapsed yet -- the
// cheap short-circuit path that lets go-scheduler's 1-second tick hammer
// this Runner without costing a real dispatch attempt between real retry
// attempts.
var ErrBackoffActive = errors.New("scheduler: schedule run is inside its backoff window")

// ErrRunTerminal is returned by RetryingRunner.Enqueue when the firing this
// Job identifies (by ScheduleID, cross-checked against RunID -- see
// getOrCreateRun's doc comment) has already reached a terminal
// schedule_runs status (succeeded or exhausted). In real production use
// this should not occur (go-scheduler's own tick() only calls Enqueue
// again for a firing that returned a non-nil error, and this layer always
// returns nil once a firing is terminal -- see Enqueue's exhaustion
// branch), but it's a real, reachable path when a caller (a test harness,
// or a stale in-flight tick landing after next_run has already advanced)
// calls Enqueue again with an unchanged Job for a firing this layer has
// already concluded.
var ErrRunTerminal = errors.New("scheduler: schedule run already reached a terminal state")

// Backoff curve (task step "pick a concrete curve," documented in
// TASKS/scheduling/04-retry-backoff-on-fail-policy.md's Work Log):
// exponential with a cap. delay(attempt) = min(BaseDelay * 2^(attempt-1),
// MaxDelay), attempt being the 1-indexed real-dispatch-attempt number that
// just failed (the delay computed is how long to wait before the *next*
// attempt). Defaults: 30s base, 5m cap -- attempt 1 -> 30s, attempt 2 ->
// 60s, attempt 3 -> 120s, attempt 4 -> 240s, attempt 5+ -> capped at 300s.
// Chosen because schedule firings are typically minutes-to-hours apart
// (cron-driven), so a sub-minute-to-a-few-minutes backoff window between
// retries of one firing is proportionate -- long enough that go-scheduler's
// 1-second hammering costs only a cheap schedule_runs lookup between real
// attempts (per the mechanism's own stated goal), short enough that a
// transient failure (a momentary downstream outage) still gets retried
// within the same rough time window most cron cadences operate at.
const (
	defaultBackoffBase = 30 * time.Second
	defaultBackoffCap  = 5 * time.Minute
)

// ScheduleRunStore is the narrow internal/store surface RetryingRunner
// needs for schedule_runs bookkeeping -- matching this package's own
// narrow-dependency-interface convention (runner_adapter.go's
// DurableAgentWaker/WorkflowLauncher/CommandExecutor/ReflexLookup).
// *store.Store satisfies this today (internal/store/schedule_runs.go).
type ScheduleRunStore interface {
	GetOpenScheduleRun(ctx context.Context, scheduleID string) (*store.ScheduleRun, error)
	GetLatestScheduleRun(ctx context.Context, scheduleID string) (*store.ScheduleRun, error)
	CreateScheduleRun(ctx context.Context, row store.ScheduleRun) (*store.ScheduleRun, error)
	RecordScheduleRunAttempt(ctx context.Context, id, status, lastError string, nextAttemptAt *time.Time) error
}

// ScheduleLookup resolves an agent_schedules row's retry policy
// (MaxRetries/OnFail) by schedule ID. *store.Store's existing
// GetAgentSchedule satisfies this.
type ScheduleLookup interface {
	GetAgentSchedule(ctx context.Context, id string) (*store.AgentSchedule, error)
}

// ScheduleDisabler is the on_fail=disable exhaustion action --
// go-scheduler.Store's own DisableSchedule method, satisfied directly by
// *StoreAdapter (this same package, task 02) with no adapting needed.
type ScheduleDisabler interface {
	DisableSchedule(ctx context.Context, id string) error
}

// RetryingRunner implements gosched.Runner, wrapping an inner Runner (03's
// RunnerAdapter in production) with schedule_runs-tracked retry/backoff/
// on_fail bookkeeping. See this file's package doc comment for the
// decorator rationale.
//
// Logger and Now follow this package's established "required dependency,
// nil is a construction bug" convention for Logger (StoreAdapter) plus an
// optional clock override for tests (defaults to time.Now when nil).
// BaseDelay/MaxDelay default to defaultBackoffBase/defaultBackoffCap when
// zero.
type RetryingRunner struct {
	Inner     gosched.Runner
	Runs      ScheduleRunStore
	Schedules ScheduleLookup
	Disabler  ScheduleDisabler
	Logger    *slog.Logger

	// Traces is TASKS/scheduling/06-schedule-fire-telemetry.md's
	// event_log sink (telemetry.go's TraceStore) -- optional, unlike
	// Runs/Schedules/Disabler/Logger: a nil Traces means
	// EmitScheduleFireTrace no-ops for every outcome (see that function's
	// own doc comment for why a nil TraceStore is tolerated here rather
	// than treated as a construction bug), so existing callers/tests that
	// have no need for schedule-fire telemetry are unaffected by this
	// field's addition. *store.Store satisfies this today.
	Traces TraceStore

	Now       func() time.Time
	BaseDelay time.Duration
	MaxDelay  time.Duration
}

var _ gosched.Runner = (*RetryingRunner)(nil)

func (r *RetryingRunner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *RetryingRunner) backoffDelay(attempt int64) time.Duration {
	base := r.BaseDelay
	if base <= 0 {
		base = defaultBackoffBase
	}
	maxDelay := r.MaxDelay
	if maxDelay <= 0 {
		maxDelay = defaultBackoffCap
	}
	if attempt < 1 {
		attempt = 1
	}
	shift := attempt - 1
	if shift > 30 { // guard time.Duration overflow; caps out long before this matters
		return maxDelay
	}
	d := base * time.Duration(int64(1)<<uint(shift))
	if d <= 0 || d > maxDelay {
		return maxDelay
	}
	return d
}

// Enqueue implements gosched.Runner. See this file's package doc comment
// for the overall mechanism; the flow here:
//
//  1. Look up (or create) the schedule_runs row for this firing
//     (getOrCreateRun). If that firing already concluded (terminal status,
//     detected via a matching RunID on the latest row -- see that
//     method's doc comment), short-circuit with ErrRunTerminal: no real
//     dispatch, no bookkeeping change.
//  2. If a backoff window is active (next_attempt_at hasn't elapsed),
//     short-circuit with ErrBackoffActive: no real dispatch.
//  3. Otherwise call the inner Runner -- the one real dispatch attempt.
//  4. gosched.ErrDuplicateJob passes through completely unaffected: no
//     schedule_runs write at all, per the task's own explicit requirement
//     that a duplicate-run race is not a retry-policy concern.
//  5. Any other error: bump attempt_count. If the schedule's own
//     max_retries is now exhausted, apply on_fail and return nil (stopping
//     go-scheduler's own retry loop -- the real outcome lives in
//     schedule_runs, not misrepresented as success to the engine).
//     Otherwise record the failure and the next backoff window, and
//     return the original error so go-scheduler keeps ticking (this
//     layer's own backoff window, not the library's, governs the real
//     retry cadence).
//  6. Success: mark the row succeeded, return nil.
//
// TASKS/scheduling/06-schedule-fire-telemetry.md: each of steps 5's two
// sub-branches (retry, exhaustion) and step 6 (success) -- the three real
// dispatch outcomes -- calls r.emitTrace immediately after its own
// RecordScheduleRunAttempt call, win or lose. Step 1's terminal
// short-circuit and step 2's backoff-window short-circuit do not (no real
// dispatch attempt happened), nor does gosched.ErrDuplicateJob's pass-
// through in step 4 (no schedule_runs write happens there either, by this
// function's own step-4 rule -- a trace row with nothing in schedule_runs
// to correlate against would be a half-recorded event). See telemetry.go's
// ScheduleFireOutcome doc comment for the full reasoning.
func (r *RetryingRunner) Enqueue(ctx context.Context, job gosched.Job) error {
	run, terminal, err := r.getOrCreateRun(ctx, job)
	if err != nil {
		return fmt.Errorf("scheduler: retrying runner get-or-create schedule_runs row (schedule %s): %w", job.ScheduleID, err)
	}
	if terminal {
		return fmt.Errorf("scheduler: schedule %s run %s already %s: %w", job.ScheduleID, run.RunID, run.Status, ErrRunTerminal)
	}

	if run.NextAttemptAt != "" {
		nextAt, perr := time.Parse(time.RFC3339, run.NextAttemptAt)
		if perr == nil && r.now().Before(nextAt) {
			return fmt.Errorf("scheduler: schedule %s in backoff window until %s: %w", job.ScheduleID, run.NextAttemptAt, ErrBackoffActive)
		}
	}

	dispatchErr := r.Inner.Enqueue(ctx, job)
	// Outcome bookkeeping must survive cancellation of the dispatch it records.
	persistCtx := context.WithoutCancel(ctx)

	if dispatchErr == nil {
		bkErr := r.Runs.RecordScheduleRunAttempt(persistCtx, run.ID, store.ScheduleRunStatusSucceeded, "", nil)
		if bkErr != nil {
			r.logger().Error("scheduler: record schedule_runs success failed", "schedule_id", job.ScheduleID, "run_id", run.ID, "error", bkErr)
		}
		r.emitTrace(ctx, job, run.ID, ScheduleFireOutcomeSuccess, run.AttemptCount+1, 0, "", nil, bkErr)
		return nil
	}

	if errors.Is(dispatchErr, gosched.ErrDuplicateJob) {
		// Pass through unchanged -- a duplicate-run race is not a
		// retry-policy concern (task step 5). No schedule_runs write:
		// this attempt never really happened from the retry budget's
		// point of view. No trace row either, by the same logic -- see
		// this method's own doc comment and telemetry.go's
		// ScheduleFireOutcome doc comment.
		return dispatchErr
	}

	maxRetries, onFail := r.retryPolicy(ctx, job.ScheduleID)
	attemptCount := run.AttemptCount + 1

	if attemptCount >= maxRetries {
		r.applyOnFail(ctx, job, onFail)
		bkErr := r.Runs.RecordScheduleRunAttempt(persistCtx, run.ID, store.ScheduleRunStatusExhausted, dispatchErr.Error(), nil)
		if bkErr != nil {
			r.logger().Error("scheduler: record schedule_runs exhaustion failed", "schedule_id", job.ScheduleID, "run_id", run.ID, "error", bkErr)
		}
		r.emitTrace(ctx, job, run.ID, ScheduleFireOutcomeExhausted, attemptCount, maxRetries, onFail, dispatchErr, bkErr)
		// Stop go-scheduler's own retry loop -- the real outcome lives in
		// schedule_runs (status=exhausted, last_error set), not
		// misrepresented to the engine as a successful dispatch.
		return nil
	}

	nextAt := r.now().Add(r.backoffDelay(attemptCount))
	bkErr := r.Runs.RecordScheduleRunAttempt(persistCtx, run.ID, store.ScheduleRunStatusFailed, dispatchErr.Error(), &nextAt)
	if bkErr != nil {
		r.logger().Error("scheduler: record schedule_runs failure failed", "schedule_id", job.ScheduleID, "run_id", run.ID, "error", bkErr)
	}
	r.emitTrace(ctx, job, run.ID, ScheduleFireOutcomeRetry, attemptCount, maxRetries, onFail, dispatchErr, bkErr)
	return dispatchErr
}

// emitTrace resolves this firing's schedule_name (only when telemetry is
// actually configured -- see below) and calls EmitScheduleFireTrace for
// one real dispatch outcome. Called from all three of Enqueue's real
// outcome branches (success, retry, exhausted); never from the
// terminal/backoff-window short-circuits or the ErrDuplicateJob
// pass-through, per this file's own package-level and Enqueue doc
// comments.
//
// The extra r.Schedules.GetAgentSchedule lookup this performs (beyond
// whatever retryPolicy already did for the retry/exhausted branches) only
// happens when r.Traces is non-nil -- i.e., exactly when telemetry is
// wired up and a caller will actually read this value. Every existing
// TASKS/scheduling/04 regression test constructs a RetryingRunner without
// setting Traces, so this adds zero additional store queries to that
// existing, already-reviewed test suite.
func (r *RetryingRunner) emitTrace(
	ctx context.Context,
	job gosched.Job,
	scheduleRunRowID string,
	outcome ScheduleFireOutcome,
	attemptCount, maxRetries int64,
	onFail string,
	dispatchErr, bkErr error,
) {
	if r.Traces == nil {
		return
	}
	EmitScheduleFireTrace(ctx, r.Traces, r.logger(), ScheduleFireTraceInput{
		Job:              job,
		ScheduleName:     r.scheduleName(ctx, job.ScheduleID),
		ScheduleRunRowID: scheduleRunRowID,
		Outcome:          outcome,
		AttemptCount:     attemptCount,
		MaxRetries:       maxRetries,
		OnFail:           onFail,
		DispatchError:    dispatchErr,
		BookkeepingError: bkErr,
	})
}

// scheduleName resolves scheduleID's agent_schedules.name for
// EmitScheduleFireTrace's detail field -- falls back to the raw
// scheduleID (never empty, unlike name -- see telemetry.go's package doc
// comment for why name, not id, is still the preferred detail value) when
// the row can't be resolved or its name is empty, mirroring retryPolicy's
// own fail-safe-default convention immediately below.
func (r *RetryingRunner) scheduleName(ctx context.Context, scheduleID string) string {
	sched, err := r.Schedules.GetAgentSchedule(ctx, scheduleID)
	if err != nil || sched == nil || sched.Name == "" {
		return scheduleID
	}
	return sched.Name
}

// getOrCreateRun resolves the schedule_runs row for job's firing.
//
// Primary correlation key: ScheduleID, not Job.RunID -- see
// ScheduleRunStore's implementation doc comment
// (internal/store/schedule_runs.go's GetOpenScheduleRun) for the full
// finding on why Job.RunID is not stable across a firing's retry attempts
// in real go-scheduler usage. If an open (non-terminal) row already exists
// for this schedule, it IS this firing's row, regardless of whether its
// stored RunID matches job.RunID (it won't, on a real retry tick).
//
// If no open row exists, this could mean either (a) this is a genuinely
// new firing (the common case), or (b) go-scheduler/a test harness is
// calling Enqueue again with an unchanged Job for a firing this layer
// already concluded (terminal). (b) is distinguished from (a) by checking
// whether the single most recent row for this schedule has the exact same
// RunID as job.RunID and is already terminal -- that specific combination
// only arises when the caller reused the identical Job value, which real
// go-scheduler ticks don't do (a fresh RunID every tick) but a simplified
// test harness simulating repeated ticking does. In that case, return the
// terminal row with terminal=true rather than creating a fresh row (which
// would both reset the retry budget for an already-concluded firing and
// collide with schedule_runs' UNIQUE INDEX on run_id).
func (r *RetryingRunner) getOrCreateRun(ctx context.Context, job gosched.Job) (run *store.ScheduleRun, terminal bool, err error) {
	open, err := r.Runs.GetOpenScheduleRun(ctx, job.ScheduleID)
	if err == nil {
		return open, false, nil
	}
	if !errors.Is(err, store.ErrScheduleRunNotFound) {
		return nil, false, err
	}

	latest, lerr := r.Runs.GetLatestScheduleRun(ctx, job.ScheduleID)
	if lerr == nil && latest.RunID == job.RunID && isTerminalRunStatus(latest.Status) {
		return latest, true, nil
	}
	if lerr != nil && !errors.Is(lerr, store.ErrScheduleRunNotFound) {
		return nil, false, lerr
	}

	firedAt := job.FiredAt
	if firedAt.IsZero() {
		firedAt = r.now()
	}
	created, cerr := r.Runs.CreateScheduleRun(ctx, store.ScheduleRun{
		ScheduleID: job.ScheduleID,
		RunID:      job.RunID,
		FiredAt:    firedAt.UTC().Format(time.RFC3339),
		Status:     store.ScheduleRunStatusPending,
	})
	if cerr != nil {
		return nil, false, cerr
	}
	return created, false, nil
}

func isTerminalRunStatus(status string) bool {
	return status == store.ScheduleRunStatusSucceeded || status == store.ScheduleRunStatusExhausted
}

// retryPolicy resolves scheduleID's MaxRetries/OnFail, defaulting to 3/
// retry (mirroring InsertAgentSchedule's own zero-value defaulting) if the
// schedule can't be read -- a schedule row disappearing mid-retry-sequence
// is not expected in practice (nothing deletes agent_schedules rows out
// from under an in-flight firing today), but failing safe here (still
// apply a bounded retry policy, log the anomaly) is preferable to a nil
// dereference or an unbounded retry loop.
func (r *RetryingRunner) retryPolicy(ctx context.Context, scheduleID string) (maxRetries int64, onFail string) {
	sched, err := r.Schedules.GetAgentSchedule(ctx, scheduleID)
	if err != nil || sched == nil {
		r.logger().Warn("scheduler: could not resolve retry policy for schedule, using defaults", "schedule_id", scheduleID, "error", err)
		return 3, store.ScheduleOnFailRetry
	}
	mr := sched.MaxRetries
	if mr <= 0 {
		mr = 3
	}
	of := sched.OnFail
	if of == "" {
		of = store.ScheduleOnFailRetry
	}
	return mr, of
}

// applyOnFail applies the schedule's on_fail policy once max_retries is
// exhausted.
//
// on_fail='retry'-at-exhaustion resolution (already pinned down by task 01,
// restated/confirmed here per this task's own instruction): 'retry' is not
// itself a meaningful terminal action once max_retries is exhausted -- it's
// what already happened for max_retries rounds. Task 01's Work Log
// resolves it to behave identically to 'notify' at exhaustion: log/emit,
// do not disable. Implemented here as a shared fall-through case, not an
// unhandled/error branch.
func (r *RetryingRunner) applyOnFail(ctx context.Context, job gosched.Job, onFail string) {
	switch onFail {
	case store.ScheduleOnFailDisable:
		if r.Disabler == nil {
			r.logger().Error("scheduler: on_fail=disable but no Disabler configured", "schedule_id", job.ScheduleID)
			return
		}
		if err := r.Disabler.DisableSchedule(ctx, job.ScheduleID); err != nil {
			r.logger().Error("scheduler: disable schedule after exhaustion failed", "schedule_id", job.ScheduleID, "error", err)
		}
	default:
		// store.ScheduleOnFailNotify, store.ScheduleOnFailRetry (01's
		// resolution), and any unrecognized value all fall through to the
		// same non-disabling notice -- see doc comment above.
		r.emitExhaustionNotice(ctx, job, onFail)
	}
}

// emitExhaustionNotice was the placeholder emission point named by
// TASKS/scheduling/06-schedule-fire-telemetry.md's own dispatch prompt,
// left in place (not deleted) once 06 landed, downgraded from
// "placeholder" to "supplementary log line": the real, structured record
// of this exact event is now Enqueue's own r.emitTrace call, one call
// site up, immediately after applyOnFail returns -- it fires for this
// on_fail branch (and, unlike this function, for on_fail=disable too),
// carries the same schedule_id/run_id/job_type/on_fail fields plus the
// dispatch error and attempt/retry-budget detail, and lands in event_log
// (category="schedule_fire") where it survives a process restart and is
// queryable, unlike this slog line. Kept anyway as a zero-cost real-time
// operational signal for anyone tailing logs, and as the one fallback
// that still fires if a RetryingRunner is ever constructed with Traces
// left nil (telemetry not wired up) -- see TraceStore's own doc comment
// for why that's a tolerated, non-error configuration state here.
func (r *RetryingRunner) emitExhaustionNotice(ctx context.Context, job gosched.Job, onFail string) {
	r.logger().Warn("scheduler: schedule exhausted retries, on_fail does not disable",
		"schedule_id", job.ScheduleID, "run_id", job.RunID, "job_type", job.JobType, "on_fail", onFail)
}

func (r *RetryingRunner) logger() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}
