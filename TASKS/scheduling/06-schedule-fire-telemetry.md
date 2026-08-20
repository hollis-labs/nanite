# Schedule-fire telemetry — reuse the reflex `event_log` pattern

**Phase:** 2 — Observability & producers (`TASKS/scheduling`)
**Status:** not-started
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

Not started.

## Review notes

<!-- Reviewer fills in. -->
