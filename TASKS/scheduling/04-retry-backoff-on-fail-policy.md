# Retry, backoff, and `on_fail` policy

**Phase:** 1 — Core engine (`TASKS/scheduling`)
**Status:** not-started
**Depends on:** `01-schema-schedule-kind-collapse-and-retry-columns.md` (`schedule_runs` table, `max_retries`/`on_fail` columns), `02-store-adapter.md` (`DisableSchedule`), `03-runner-adapter-and-job-taxonomy.md` (wraps its `Enqueue`).
**Touches:** the same package as `02`/`03` (wraps the Runner adapter's `Enqueue`, or is layered as a decorator around it — see step 1), `internal/store/` (new `schedule_runs` read/write helpers, if `01` didn't already build them — check `01`'s Work Log first).

## Context

`docs/engineering/architecture/12-scheduling.md`'s "Retry, backoff, and `on_fail` policy" section is the design, in full — read it before starting. The core problem it solves: `go-scheduler` itself only offers "retry the same firing every second, forever, uncounted" (confirmed directly by the design session against `engine.go`'s `SetScheduleNextRun` rollback-on-failure behavior) — real retry/backoff/failure policy has to live entirely in this layer, not the library.

**The mechanism, already decided — implement it, don't redesign it:**
- On `Enqueue` failure, check `schedule_runs` for the current firing (`job.RunID`): if `attempt_count < max_retries` (the schedule's own column, from `01`) and `next_attempt_at` hasn't elapsed yet, return the same error again immediately **without a real dispatch attempt** — cheap, so `go-scheduler`'s 1-second hammering costs only a `schedule_runs` lookup between real attempts, while the actual backoff cadence is owned by `next_attempt_at`, not the library's tick rate.
- Once `attempt_count >= max_retries`, apply the schedule's `on_fail` policy: `disable` calls `Store.DisableSchedule` directly; `notify` emits an event (this task emits the event — full observability coverage is `06-schedule-fire-telemetry.md`'s job, but this task's `notify` branch must call into whatever emission point `06` builds, or a placeholder this task documents clearly if `06` hasn't landed yet) without disabling. Then **return `nil`** — signaling success to `go-scheduler` purely to stop its retry loop, with the real outcome recorded in `schedule_runs`, not misrepresented to the engine as a successful dispatch. (`retry` is not a valid terminal `on_fail` value at exhaustion — by definition it's what already happened for `attempt_count` rounds; confirm `01`'s schema Work Log for how the `on_fail='retry'` default value is meant to behave at exhaustion, since a schedule configured `on_fail='retry'` still needs *some* terminal behavior once `max_retries` is hit — document your resolution if `01` didn't already pin this down.)

**The backoff curve itself is explicitly not decided by the design doc — this task decides it, documents it, doesn't treat it as free-form.** Pick a concrete curve (e.g. exponential with a cap, matching a common convention — or linear, your call) and document the exact formula and any cap in this file's Work Log. This is a real implementation decision this task file is deliberately deferring to you, not an oversight.

## What to do

1. Decide whether this is a decorator wrapping `03`'s `Runner` (implementing `gosched.Runner` itself, delegating to the inner Runner on the real dispatch attempt) or logic folded directly into `03`'s `Enqueue`. A decorator (`retryingRunner{inner gosched.Runner, store ScheduleRunStore}`) keeps `03`'s own per-job-type dispatch logic and this task's retry/backoff bookkeeping independently testable — recommended, but not mandatory; document your call.
2. On every `Enqueue(ctx, job)` call: look up (or create, if this is the firing's first attempt) the `schedule_runs` row keyed by `job.RunID`. If a backoff window is active (per the mechanism above), short-circuit without calling the inner Runner. Otherwise call the inner Runner, record the outcome (`attempt_count++`, `last_error`, `next_attempt_at` per your chosen curve, `status`) in `schedule_runs`.
3. On exhaustion (`attempt_count >= max_retries`), apply `on_fail` per the mechanism above and mark the `schedule_runs` row `status='exhausted'` (or your `01`-aligned vocabulary).
4. On success, mark the `schedule_runs` row `status='succeeded'` (or equivalent) — this is what lets `06-schedule-fire-telemetry.md` and any future operator-facing view distinguish a clean fire from a retried-then-succeeded one.
5. Translate `gosched.ErrDuplicateJob` (from the inner Runner, per `03`'s own translation) through unchanged — a duplicate-run race is not a retry-policy concern, don't let this layer's bookkeeping misinterpret it as a failed attempt.

## Done means

- A regression test proves: a schedule with `max_retries=3` whose inner Runner always fails gets exactly 3 real dispatch attempts (not 4, not 1), then the `on_fail` policy applies and no further real attempts happen even as `go-scheduler` keeps ticking (simulate via repeated `Enqueue` calls with the backoff window collapsed for test speed, or inject a fake clock).
- A regression test proves `on_fail='disable'` at exhaustion calls `Store.DisableSchedule` exactly once; `on_fail='notify'` does not disable.
- A regression test proves a between-attempts `Enqueue` call inside the backoff window returns an error without invoking the inner Runner (cheap short-circuit, not a real dispatch).
- A regression test proves `gosched.ErrDuplicateJob` passes through unaffected by retry bookkeeping.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Your decorator-vs-inline call (step 1), your backoff curve formula and cap, and your `on_fail='retry'`-at-exhaustion resolution are documented in this file's Work Log.

## Work log

Not started.

## Review notes

<!-- Reviewer fills in. -->
