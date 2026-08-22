// This file (telemetry.go) implements
// TASKS/scheduling/06-schedule-fire-telemetry.md and
// docs/engineering/architecture/12-scheduling.md's "Observability" section:
// go-scheduler's own Status{} counter snapshot (four monotonic ints, no
// per-schedule detail, no logging) is too coarse to answer "did this
// schedule fire, when, why" on its own, so schedule fires reuse the
// existing event_log telemetry pattern instead of a second observability
// surface.
//
// This is the same reuse decision TASKS/harness-reactive-self-tools/
// 05-selftool-reaction-telemetry.md already made for self-tool reactions
// (internal/selftools/reactions/telemetry.go, read in full before writing
// this file) -- and both, in turn, mirror internal/agent/reflexes/
// telemetry.go's EmitFirings/TraceStore split in *shape* only. All three
// are deliberately parallel, non-sharing thin wrappers around the same
// event_log sink (internal/store/events.go), each with its own distinct
// category so the three streams stay independently queryable via the
// existing (*store.Store).ListEvents(category, limit) path:
//   - "reflex"             (internal/agent/reflexes/telemetry.go)
//   - "selftool_reaction"  (internal/selftools/reactions/telemetry.go)
//   - "schedule_fire"      (this file)
//
// Field mapping (docs/engineering/architecture/12-scheduling.md's
// Observability section, and this task's own Context, verbatim):
//   - event_type = the job type (durable_agent_wake, agent_workflow_run,
//     command_run, reflex_dispatch) -- gosched.Job.JobType, already the
//     exact string runner_adapter.go's JobType* constants define.
//   - category   = CategoryScheduleFire ("schedule_fire").
//   - detail     = the schedule's name (agent_schedules.name). Checked
//     directly against 071_agent_schedules.sql before choosing this over
//     id: name is NOT NULL but NOT UNIQUE -- the same non-uniqueness
//     reflexes' own EmitFirings already accepts for its own detail=r.Name
//     (agent_reflexes.name carries no UNIQUE constraint either). Chosen
//     anyway because (a) it's the human-readable value an operator
//     scanning event_log actually wants, matching the reflex precedent,
//     and (b) the genuinely-unique schedule_id is carried in full inside
//     metadata.schedule_id regardless, so no disambiguating capability is
//     lost -- a caller that needs an exact match filters/greps metadata,
//     the same way a reflex-name collision would already require today.
//   - metadata   = a traceRecord: schedule_id, schedule_name, run_id
//     (gosched.Job.RunID -- NOT schedule_runs.id; see traceRecord's own
//     doc comment for why both id shapes are carried), job_type, the
//     payload used, attempt_count, max_retries/on_fail (populated for the
//     retry and exhausted outcomes, omitted for success), success/error,
//     and -- new relative to the task's own literal field list, see this
//     file's Work Log entry for TASKS/scheduling/06 -- bookkeeping_error,
//     folding in the adjacent, already-disclosed gap TASKS/scheduling/
//     04's review flagged (schedule_runs write failures were previously
//     logged-and-continue with nothing surfaced beyond a slog.Error line).
package scheduler

import (
	"context"
	"encoding/json"
	"log/slog"

	gosched "github.com/hollis-labs/go-scheduler"
)

// CategoryScheduleFire is the event_log.category value every
// EmitScheduleFireTrace row uses -- confirmed via direct grep of every
// existing (*store.Store).LogEvent call site and category literal in this
// codebase before locking the value (this task's own item 3): distinct
// from reflexes' "reflex" (internal/agent/reflexes/telemetry.go) and
// self-tool reactions' "selftool_reaction"
// (internal/selftools/reactions/telemetry.go), and not otherwise in live
// use anywhere ("error", "tool", "context", "performance", "recovery",
// "info", "warning" are the other categories a repo-wide grep turns up).
const CategoryScheduleFire = "schedule_fire"

// ScheduleFireOutcome enumerates the three real dispatch outcomes
// RetryingRunner.Enqueue can reach for a given firing -- exactly the three
// scenarios this task's own Done-means regression test names.
//
// Deliberately does NOT include a fourth value for the short-circuited
// backoff-window skip or the gosched.ErrDuplicateJob pass-through: this
// task's own explicit design call (see Work Log) is that only real
// dispatch attempts -- the three outcomes below -- get a trace row. A
// backoff-window skip is a cheap table lookup with zero dispatch attempt
// (RetryingRunner.Enqueue's own doc comment, step 2); tracing it would
// mean one event_log row roughly every second for the entire length of a
// schedule's backoff window (up to defaultBackoffCap = 5 minutes today),
// which is exactly the flooding the architecture doc's own suggested
// default warns against. ErrDuplicateJob is, by the same file's own
// explicit doc comment, "not a retry-policy concern" and gets no
// schedule_runs write either -- a trace row with no corresponding
// bookkeeping row would be a half-recorded event with nothing in
// schedule_runs to correlate it against.
type ScheduleFireOutcome string

const (
	// ScheduleFireOutcomeSuccess is a real dispatch attempt that
	// succeeded -- the schedule_runs row transitions to 'succeeded'
	// (terminal).
	ScheduleFireOutcomeSuccess ScheduleFireOutcome = "success"
	// ScheduleFireOutcomeRetry is a real dispatch attempt that failed but
	// has budget remaining (attempt_count < max_retries) -- the
	// schedule_runs row transitions to 'failed' (non-terminal, a further
	// retry attempt is scheduled via next_attempt_at).
	ScheduleFireOutcomeRetry ScheduleFireOutcome = "retry"
	// ScheduleFireOutcomeExhausted is a real dispatch attempt that failed
	// and exhausted the schedule's retry budget (attempt_count >=
	// max_retries) -- the schedule_runs row transitions to 'exhausted'
	// (terminal), and the schedule's on_fail policy has already been
	// applied by the time this outcome is traced.
	ScheduleFireOutcomeExhausted ScheduleFireOutcome = "exhausted"
)

// TraceStore is the narrow persistence surface EmitScheduleFireTrace
// needs -- matching internal/agent/reflexes/telemetry.go:56-60's pattern
// (a two-method interface there, because reflexes also bump fired_count;
// one method here, matching internal/selftools/reactions/telemetry.go's
// own TraceStore, since schedule-fire telemetry has no fired_count-style
// bump of its own to carry -- see this task's Work Log for the
// agent_schedules.fired_count investigation that confirms this isn't an
// oversight). *store.Store (internal/store/events.go) satisfies this
// directly.
type TraceStore interface {
	LogEvent(ctx context.Context, sessionID, eventType, category, detail, metadata string)
}

// traceRecord is the one consistent shape every EmitScheduleFireTrace row
// emits to event_log.metadata -- built from data RetryingRunner.Enqueue
// already computed (see ScheduleFireTraceInput), no re-evaluation of any
// retry/backoff decision happens here.
type traceRecord struct {
	ScheduleID   string `json:"schedule_id"`
	ScheduleName string `json:"schedule_name,omitempty"`
	// RunID is gosched.Job.RunID for the specific tick that produced this
	// outcome -- NOT schedule_runs.id (that PK is carried separately as
	// ScheduleRunRowID, since RetryingRunner's own doc comment establishes
	// Job.RunID is regenerated every tick and is therefore NOT stable
	// across one firing's retry attempts; a retried-then-succeeded firing
	// will show a different run_id on its "retry" row than on its
	// "success" row, by design, while schedule_run_row_id stays the same
	// across both -- the field an operator should actually correlate a
	// firing's full retry history by).
	RunID            string `json:"run_id"`
	ScheduleRunRowID string `json:"schedule_run_row_id,omitempty"`
	JobType          string `json:"job_type"`
	// Payload is the exact gosched.Job.Payload bytes used for this
	// dispatch attempt, embedded as a nested JSON value (not a
	// double-escaped string) -- mirroring internal/selftools/reactions/
	// telemetry.go's traceRecord.Config precedent for the same shape of
	// already-JSON payload.
	Payload      json.RawMessage `json:"payload,omitempty"`
	Outcome      string          `json:"outcome"`
	AttemptCount int64           `json:"attempt_count"`
	// MaxRetries/OnFail are populated for the retry and exhausted
	// outcomes (where they were actually consulted to reach this
	// decision) and omitted for success (where they were not).
	MaxRetries int64  `json:"max_retries,omitempty"`
	OnFail     string `json:"on_fail,omitempty"`
	// Error is the dispatch error's own message -- present for retry and
	// exhausted, absent for success. For the exhausted outcome combined
	// with OnFail, this is deliberately the whole of "enough detail for
	// an operator to understand why the schedule stopped retrying without
	// a separate schedule_runs query" the architecture doc's field
	// mapping asks for: what failed (Error), how many times (AttemptCount
	// / MaxRetries), and what happened as a result (OnFail).
	Error string `json:"error,omitempty"`
	// BookkeepingError is set only when RetryingRunner's own
	// RecordScheduleRunAttempt call for this same outcome itself failed --
	// see this file's Work Log entry on TASKS/scheduling/04's review
	// finding (schedule_runs write failures were previously
	// logged-and-continue with nothing surfaced beyond slog.Error). This
	// does not change what Outcome/DispatchError this row reports (the
	// real dispatch outcome, decided before the bookkeeping write was
	// even attempted) -- it's an additional, independent signal that the
	// schedule_runs row itself may not reflect this outcome correctly.
	BookkeepingError string `json:"bookkeeping_error,omitempty"`
}

// ScheduleFireTraceInput carries everything EmitScheduleFireTrace needs
// beyond a TraceStore -- all of it already computed by
// RetryingRunner.Enqueue by the time it calls this function, mirroring
// internal/agent/reflexes/telemetry.go's FiringContext split (caller-local
// context assembled by the one real caller, not re-derived here).
type ScheduleFireTraceInput struct {
	Job              gosched.Job
	ScheduleName     string
	ScheduleRunRowID string
	Outcome          ScheduleFireOutcome
	AttemptCount     int64
	MaxRetries       int64
	OnFail           string
	DispatchError    error
	BookkeepingError error
}

// EmitScheduleFireTrace writes one event_log row for a single real
// schedule-dispatch outcome (see ScheduleFireOutcome's doc comment for
// which outcomes qualify). A no-op when ts is nil -- schedule-fire
// telemetry is an optional capability of RetryingRunner (its own Traces
// field may be left unconfigured, e.g. by a caller/test that has no need
// for it), not a required one, so a nil TraceStore here is not treated as
// a caller-programming error the way internal/selftools/reactions/
// telemetry.go's EmitReactionTrace treats it (that package's Fire/Result
// pipeline has no equivalent "telemetry wasn't wired up yet" construction
// state to tolerate). ctx is accepted, not currently used by TraceStore's
// single method, kept for signature consistency with the other two
// telemetry wrappers and for future extension (matching
// EmitReactionTrace's own unused-ctx precedent).
func EmitScheduleFireTrace(ctx context.Context, ts TraceStore, logger *slog.Logger, in ScheduleFireTraceInput) {
	if ts == nil {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}

	rec := traceRecord{
		ScheduleID:       in.Job.ScheduleID,
		ScheduleName:     in.ScheduleName,
		RunID:            in.Job.RunID,
		ScheduleRunRowID: in.ScheduleRunRowID,
		JobType:          in.Job.JobType,
		Outcome:          string(in.Outcome),
		AttemptCount:     in.AttemptCount,
		MaxRetries:       in.MaxRetries,
		OnFail:           in.OnFail,
	}
	if len(in.Job.Payload) > 0 && json.Valid(in.Job.Payload) {
		rec.Payload = json.RawMessage(in.Job.Payload)
	}
	if in.DispatchError != nil {
		rec.Error = in.DispatchError.Error()
	}
	if in.BookkeepingError != nil {
		rec.BookkeepingError = in.BookkeepingError.Error()
	}

	metaJSON, err := json.Marshal(rec)
	if err != nil {
		logger.Warn("scheduler.EmitScheduleFireTrace: marshal trace record failed",
			"schedule_id", in.Job.ScheduleID, "run_id", in.Job.RunID, "outcome", in.Outcome, "err", err)
		metaJSON = []byte("{}")
	}

	detail := in.ScheduleName
	if detail == "" {
		detail = in.Job.ScheduleID
	}

	// Outcome bookkeeping must survive cancellation of the dispatch it records.
	ts.LogEvent(context.WithoutCancel(ctx), "", in.Job.JobType, CategoryScheduleFire, detail, string(metaJSON))
}
