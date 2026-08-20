# Schedule-fire telemetry — reuse the reflex `event_log` pattern

**Phase:** 2 — Observability & producers (`TASKS/scheduling`)
**Status:** implemented
**Depends on:** `03-runner-adapter-and-job-taxonomy.md` (needs a dispatch-outcome hook to attach telemetry to). Sequence after `05-engine-wiring-and-full-replace.md` for live verification, but no hard code dependency on it.
**Touches:** the same package as `02`/`03`/`04` (a thin wrapper, called from the Runner/retry layer), `internal/store/` (only if `LogEvent` needs anything beyond what already exists for reflexes — likely not, see Context).

## Context

`docs/engineering/architecture/12-scheduling.md`'s "Observability" section is the design: `go-scheduler`'s own `Status{Running, LastTickAt, Dispatches, WorkerErrors}` (`libs/go-scheduler/engine.go:25-30`) is too coarse to rely on alone — four monotonic counters, no per-schedule detail, no logging anywhere in the library. Rather than building a second observability surface, schedule fires reuse the **existing reflex `event_log`/telemetry pattern** (`internal/agent/reflexes/telemetry.go`'s `EmitFirings`, read it in full before starting) — one consistent place operators already look for "did this fire, when, why."

This mirrors exactly the same reuse decision `TASKS/harness-reactive-self-tools/05-selftool-reaction-telemetry.md` already made for self-tool reactions — read that task file's own Work Log once it lands (or its task file now, if not yet executed) for the concrete pattern: a new, parallel thin wrapper (not an extension of `internal/agent/reflexes/telemetry.go` itself), same `event_log` sink, a new distinct `category` value so the streams stay separately queryable.

**Field mapping, following the same convention:**
- `event_type` = the job type (`durable_agent_wake`, `agent_workflow_run`, `command_run`, `reflex_dispatch`)
- `category` = `"schedule_fire"` — new value, distinct from `"reflex"` and `"selftool_reaction"`
- `detail` = the schedule's `name` (or `id`, if `name` isn't reliably unique/descriptive — check `agent_schedules.name`'s actual usage before choosing)
- `metadata` = a trace record: schedule ID, `run_id`, job type, the payload used, attempt count (from `04`'s `schedule_runs` bookkeeping), success/error, and — for a `notify`-`on_fail` exhaustion — enough detail for an operator to understand why the schedule stopped retrying without having to separately query `schedule_runs`.

`Engine.Status()` is still worth exposing (via `09-operator-http-api.md`) as a coarse liveness signal, but it supplements per-firing event rows, it doesn't replace them — this task's own job is the per-firing rows, not the `Status()` plumbing.

## What to do

1. Build `EmitScheduleFireTrace(ctx, store TraceStore, ...)` (or equivalent name — match this codebase's own naming for the analogous reflex/self-tool-reaction functions) in the same package `02`/`03`/`04` live in, called from `04`'s retry/backoff layer at the point where an outcome (success, retry-attempt, exhaustion) is known — one `event_log` row per real dispatch attempt at minimum (document whether short-circuited backoff-window skips also get a row, or only real attempts do — the design doc doesn't pin this down explicitly; a reasonable default is real attempts only, to avoid flooding `event_log` with once-a-second no-op entries during a long backoff window, but document your call).
2. Define a narrow `TraceStore` interface for this wrapper's own needs (matching `internal/agent/reflexes/telemetry.go:56-60`'s pattern) — likely just `LogEvent`, no `fired_count`-style bump (that's `agent_schedules.fired_count`, already bumped elsewhere — check whether it's bumped by `02`'s Store adapter or needs bumping here; don't double-bump it from two places).
3. Confirm the `"schedule_fire"` category is genuinely new and doesn't collide with `"reflex"`/`"selftool_reaction"` or anything else already in live use (grep `event_log`'s `category` values before locking the name).

## Done means

- A regression test proves a successful job dispatch, a retried-then-succeeded dispatch, and an exhausted (`on_fail` applied) dispatch each produce correctly-distinguished `event_log` rows at `category = "schedule_fire"`.
- A regression test proves the three telemetry streams (`"reflex"`, `"selftool_reaction"`, `"schedule_fire"`) are independently queryable via the existing category-filtered event-log read path with no cross-contamination.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Your short-circuited-backoff-skip inclusion/exclusion call and your `fired_count`-double-bump check (step 2) are documented in this file's Work Log.

## Work log

**Pre-work correction of the task's own framing, per this codebase's
"decision vs. rationale" convention:** the task file's own Context says
"only the notify/retry-at-exhaustion path has a documented placeholder
(`emitExhaustionNotice`)... the two other outcomes... have no placeholder
at all yet." Read `Enqueue` in full, as instructed, before writing any
code: confirmed all three real-outcome branches (success at
`retrying_runner.go`'s original lines ~199-204, retry at ~228-232,
exhaustion at ~217-225) each independently call
`r.Runs.RecordScheduleRunAttempt`, and only the exhaustion branch's
`applyOnFail`→default-case call to `emitExhaustionNotice` had any
telemetry-shaped placeholder at all. This matches the task's own framing
exactly — no correction needed here, just confirming the map before
touching code. `EmitScheduleFireTrace` is now called from all three real
outcome branches, plus a fourth internal helper (`emitTrace`) that both
branches share.

**Package/file placement:** `internal/scheduler/telemetry.go` — same
package as `02`/`03`/`04`, a new file (not an extension of
`retrying_runner.go`), matching `05-selftool-reaction-telemetry.md`'s own
"new, parallel thin wrapper" precedent (that one lives in a separate
package, `internal/selftools/reactions`, because self-tool reactions
*are* a separate package from reflexes; schedule-fire telemetry has no
such separate-package need since it's already scoped to
`internal/scheduler`, so "parallel" here means "new file, not a shared
one," not "new package").

**`TraceStore` interface:** exactly one method,
`LogEvent(sessionID, eventType, category, detail, metadata string)`,
matching `*store.Store`'s real signature (`internal/store/events.go:17`)
byte-for-byte and mirroring `internal/selftools/reactions/telemetry.go`'s
own one-method `TraceStore` (not reflexes' two-method version) — no
`fired_count`-style bump belongs on this interface (see the `fired_count`
investigation below).

**`EmitScheduleFireTrace` signature:** `EmitScheduleFireTrace(ctx
context.Context, ts TraceStore, logger *slog.Logger, in
ScheduleFireTraceInput)` — void, not `error`-returning. Deliberate
divergence from `internal/selftools/reactions.EmitReactionTrace`'s
"return an error when `ts` is nil" convention: `RetryingRunner.Traces` is
an *optional* field (see below), so a nil `TraceStore` here is an
expected, tolerated configuration state, not a caller-programming error —
matching `internal/agent/reflexes/telemetry.go`'s `EmitFirings`, which
also silently no-ops on a nil `ts` rather than surfacing an error.

**Field mapping, exactly as specced, with one addition:**
- `event_type` = `job.JobType` (the four job-type constants from `03`).
- `category` = `CategoryScheduleFire = "schedule_fire"` — confirmed
  genuinely new via a full repo grep of every `LogEvent`/`category`
  literal before locking it (item 3): no collision with `"reflex"`,
  `"selftool_reaction"`, `"error"`, `"tool"`, `"context"`, `"performance"`,
  `"recovery"`, `"info"`, `"warning"`.
- `detail` = the schedule's `name`. Checked `071_agent_schedules.sql`
  directly first: `name` is `NOT NULL` but carries no `UNIQUE`
  constraint — the same non-uniqueness reflexes' own `EmitFirings`
  already accepts for its own `detail = r.Name` (`agent_reflexes.name`
  isn't unique either). Chose `name` over `id` anyway: it's the
  human-readable value an operator scanning `event_log` wants, and the
  genuinely-unique `schedule_id` is carried in full inside
  `metadata.schedule_id` regardless, so no disambiguating capability is
  lost.
- `metadata` = a `traceRecord`: `schedule_id`, `schedule_name`, `run_id`
  (`gosched.Job.RunID` — **not** `schedule_runs.id`, see below),
  `schedule_run_row_id` (`schedule_runs.id`, the field an operator should
  actually correlate a firing's full retry history by, since `run_id`
  changes every tick per `04`'s own documented finding), `job_type`,
  `payload` (the exact `gosched.Job.Payload` bytes, embedded as a nested
  JSON value), `outcome`, `attempt_count`, `max_retries`/`on_fail`
  (populated for retry/exhausted, omitted for success), `error`
  (dispatch error message), and — the one addition beyond the task's
  literal field list — `bookkeeping_error` (see below).

**`fired_count` double-bump check (task item 4/step 2) — investigated,
not assumed, and a real gap found:** grepped every call site of
`BumpAgentScheduleFireCount`. It has exactly one production caller:
`durable_wake.go`'s `RunDue`, itself only reachable today via the
still-live manual admin endpoint `POST /api/durable-agent-wake/run-due`
(confirmed via `main.go`'s own comment: `05` retired the
`"durable-agent-wake-tick"` 2-minute ticker "in full — not commented out,
deleted"). Neither `StoreAdapter` (`ClaimAndUpdateScheduleRun`/
`SetScheduleNextRun`/`DisableSchedule`) nor `RunnerAdapter` nor
`RetryingRunner` call it anywhere. **This means `agent_schedules.fired_count`
is not bumped anywhere along the real go-scheduler `Engine` dispatch
path** — every schedule fired through the new engine leaves `fired_count`
permanently at 0 (or whatever the legacy ticker last left it at, for the
one pre-existing managed row). This is a real, confirmed gap in `02`-`05`,
not a rationale mismatch to second-guess — but it is **not this task's
job to fix**, per the task's own explicit framing ("likely not your job,
but confirm rather than assume") and this task's own scope (telemetry,
not schedule bookkeeping). Not fixed here; flagging for a follow-up task
(bumping `fired_count` from either `StoreAdapter.ClaimAndUpdateScheduleRun`
or `RetryingRunner`'s success branch — the latter is more correct, since
a schedule is only meaningfully "fired" once a real dispatch attempt
resolves, not merely claimed). Confirms the double-bump risk item 4 asked
about does not exist today (there is zero bumping to double), so this
task adds none.

**`schedule_runs` write-failure surfacing gap (from `TASKS/ESCALATIONS.md`'s
2026-08-20 "Scheduling Phase 1 core" entry) — addressed, folded into the
same trace row rather than a separate mechanism:** each of `Enqueue`'s
three `RecordScheduleRunAttempt` calls now captures its own return value
(`bkErr`) and passes it straight into `r.emitTrace`'s `BookkeepingError`
parameter, surfaced in the emitted row's `metadata.bookkeeping_error`
field (`omitempty`, absent on the overwhelmingly common "write succeeded"
case). Deliberately **not** a separate `event_log` row or a distinct
category/event_type: the failure is tightly coupled to the exact outcome
row it occurred alongside (the same dispatch decision, the same
schedule/run identifiers), so folding it into that one row's metadata
gives an operator the full picture in one place rather than requiring a
join across two rows. The underlying `slog.Error` call at each of the
three sites is left untouched (still logs immediately, regardless of
whether `Traces` is configured) — this is additive visibility, not a
replacement for the existing log-and-continue behavior, and does not
change `RetryingRunner`'s control flow (a bookkeeping write failure still
does not change what outcome is reported to `go-scheduler` or recorded as
`Outcome`/`DispatchError` in the trace row — the real dispatch outcome,
decided independently of the bookkeeping write's own success).

**Backoff-window-skip / duplicate-job inclusion call (task item 3/step
"document whether short-circuited backoff-window skips... also get a
row"):** excluded, taking the design doc's own suggested default.
`ScheduleFireOutcome` has exactly three values (success/retry/exhausted)
— `ErrBackoffActive` short-circuits (step 2 of `Enqueue`) and
`gosched.ErrDuplicateJob` pass-throughs (step 4) get **no** trace row,
matching the fact that neither gets a `schedule_runs` write either (a
trace row with nothing in `schedule_runs` to correlate against would be a
half-recorded event). Locked in by two dedicated regression tests
(`TestRetryingRunner_ScheduleFireTelemetry_BackoffWindowSkip_NoTraceRow`,
`TestRetryingRunner_ScheduleFireTelemetry_DuplicateJob_NoTraceRow`), not
just documented in prose.

**`emitExhaustionNotice` (04's placeholder) — kept, reframed, not
deleted:** `04`'s own doc comment explicitly named this the "exact call
site 06 should replace with a real `EmitScheduleFireTrace(...)`-style
event_log row." The real replacement is `Enqueue`'s own `r.emitTrace`
call in the exhaustion branch (one call site up from
`emitExhaustionNotice`, immediately after `applyOnFail` returns) — it
covers this exact case (and, going further than the placeholder did,
`on_fail=disable` too, which `emitExhaustionNotice` never touched at
all). `emitExhaustionNotice` itself is left in place, doc comment updated
to describe it as a supplementary, zero-cost real-time log line — useful
for anyone tailing logs, and the one signal that still fires if a
`RetryingRunner` is ever constructed with `Traces` left nil.

**What was built:**
- `internal/scheduler/telemetry.go` — `CategoryScheduleFire`,
  `ScheduleFireOutcome` (+3 constants), `TraceStore`, `traceRecord`,
  `ScheduleFireTraceInput`, `EmitScheduleFireTrace`.
- `internal/scheduler/retrying_runner.go` — added `Traces TraceStore`
  field (optional; nil-safe); `Enqueue`'s three real-outcome branches now
  capture their `RecordScheduleRunAttempt` error and call the new
  `r.emitTrace` helper; new `emitTrace`/`scheduleName` private methods.
  `scheduleName` does its own `GetAgentSchedule` lookup **only when
  `r.Traces != nil`** (called from inside `emitTrace`, which no-ops
  first) — every existing `04` regression test constructs a
  `RetryingRunner` without setting `Traces`, so this adds zero additional
  store queries to that already-reviewed suite; confirmed by re-running
  it unmodified (all pass, same call counts as before).
- `cmd/nanite/main.go` — wired `Traces: s` into the production
  `RetryingRunner` construction (`*store.Store` satisfies `TraceStore`
  directly, no adapter needed).
- `internal/scheduler/telemetry_test.go` — 7 new tests, all driving
  telemetry through `RetryingRunner.Enqueue` (the real call site) rather
  than calling `EmitScheduleFireTrace` directly, except the one dedicated
  nil-`TraceStore` unit test:
  - `TestRetryingRunner_ScheduleFireTelemetry_Success`
  - `TestRetryingRunner_ScheduleFireTelemetry_RetryThenSuccess_TwoDistinctRows`
    (also asserts the retry/success rows share `schedule_run_row_id` but
    have different `run_id`s, proving the `run_id` vs.
    `schedule_run_row_id` distinction documented above)
  - `TestRetryingRunner_ScheduleFireTelemetry_Exhausted`
  - `TestRetryingRunner_ScheduleFireTelemetry_BackoffWindowSkip_NoTraceRow`
  - `TestRetryingRunner_ScheduleFireTelemetry_DuplicateJob_NoTraceRow`
  - `TestScheduleFireTelemetry_IndependentlyQueryableAcrossThreeStreams`
    (extends `05`'s own two-stream cross-contamination proof to all three
    live streams: `"reflex"`, `"selftool_reaction"`, `"schedule_fire"`,
    synthesizing the first two directly via `(*store.Store).LogEvent`
    rather than importing `internal/agent/reflexes`/
    `internal/selftools/reactions`, matching `05`'s own documented
    dependency-surface reasoning)
  - `TestEmitScheduleFireTrace_NilTraceStore_NoOp`

**Build/vet/test results** (run from this worktree):
- `go build ./cmd/nanite/` — passes.
- `go vet ./...` — same pre-existing, unrelated `internal/service/container.go`
  `stopReaper`/`stopRuntimeReaper` possible-context-leak findings named
  in `05-selftool-reaction-telemetry.md`'s own Work Log; `go vet
  ./internal/scheduler/...` alone is clean.
- `go test ./internal/scheduler/...` — all 43 tests pass (7 new + all
  pre-existing `02`/`03`/`04` tests unaffected), verified individually via
  `-json` output (zero `"Action":"fail"` entries).
- `go test ./...` — full suite, 103 packages, zero `"Action":"fail"`
  entries.

## Review notes

<!-- Reviewer fills in. -->
