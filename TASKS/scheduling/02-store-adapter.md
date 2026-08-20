# Store adapter — `go-scheduler.Store` over `agent_schedules`

**Phase:** 1 — Core engine (`TASKS/scheduling`)
**Status:** not-started
**Depends on:** `01-schema-schedule-kind-collapse-and-retry-columns.md` (needs the narrowed `schedule_kind` CHECK and the new `max_retries`/`on_fail` columns to exist, since `ListDueSchedules`'s conversion and the claim logic both read the real row shape).
**Touches:** new package/file (`internal/scheduler/store_adapter.go` or similar — see step 1 for the naming call), `internal/store/agent_schedules.go` (only if a new low-level DB method is needed beyond what already exists — see step 2).

## Context

`docs/engineering/architecture/12-scheduling.md`'s "The Store adapter" section is the design — read it in full, plus `libs/go-scheduler/scheduler.go:44-64`'s exact `Store` interface (four methods: `ListDueSchedules`, `ClaimAndUpdateScheduleRun`, `SetScheduleNextRun`, `DisableSchedule`) and `libs/go-scheduler/scheduler.go:24-32`'s neutral `Schedule` type (`ID`, `CronExpr`, `LastRun`, `NextRun`, `Enabled`, `JobType`, `Payload []byte`) before starting.

**The safety argument, already established, do not re-derive it — apply it:** `internal/store/store.go`'s `New()` opens the database via `sqlitekit.OpenSingle` — a single-connection pool with WAL, `busy_timeout(5s)`, and `_txlock=immediate` on every connection (`internal/store/store.go:35-39`). A single writer connection makes `UPDATE agent_schedules SET last_run=?, next_run=? WHERE id=? AND next_run=?` checking `RowsAffected()==1` safe by construction, in-process and cross-process — this is the CAS claim `ClaimAndUpdateScheduleRun` needs, and no new write-serialization infrastructure is needed to get it.

**Template to follow, directly transferable, not hypothetical:** `/Users/chrispian/dev/hollis-labs/apps/hadron/internal/scheduler/adapter.go`'s `storeAdapter` — Hadron's own `persistence.Store` already implements `ClaimAndUpdateScheduleRun`/`SetScheduleNextRun`/`DisableSchedule` with matching signatures and promotes them directly via struct embedding (`type storeAdapter struct { *persistence.Store }`); only `ListDueSchedules` needs an explicit conversion wrapper (DB record → neutral `gosched.Schedule`, packing app-specific fields into the opaque `Payload`). Confirm whether Nanite's `*store.Store` can follow the same embedding shortcut (i.e., whether it's worth adding `ClaimAndUpdateScheduleRun`/`SetScheduleNextRun`/`DisableSchedule` methods directly onto `*store.Store` with matching signatures, so a thin adapter can embed it the same way) or whether a fully separate wrapper type is cleaner given `*store.Store`'s existing method surface — either is acceptable, document your call.

## What to do

1. Decide and document the package location for this adapter. `internal/scheduler` (a new top-level package, mirroring Hadron's own `internal/scheduler`) is the natural default — matching this repo's flat-sibling `internal/` convention (`mcp`, `plugin`, `messaging`, `reflexes`, …). Confirm no existing `internal/scheduler`-shaped concept already exists under a different name before creating it (grep first).
2. Implement `ListDueSchedules(ctx, now, limit) ([]gosched.Schedule, error)`: query `agent_schedules` for enabled, due (`next_run <= now`) rows up to `limit`, converting each `store.AgentSchedule` into `gosched.Schedule` (`next_run`/`last_fired_at` — both added/repurposed by `01` — parse to `time.Time` for `NextRun`/`LastRun`; `JobType` copies straight from the row's `job_type` column). For `Payload`: `01` deliberately left `job_payload` at its `'{}'` default for every `durable_agent_wake` row (the only job type in live use today) rather than requiring a backfill — for that job type, marshal `{"body": row.Body}` as the neutral `Payload` here in the adapter; for the other three job types, marshal the row's `job_payload` column directly. Coordinate the exact JSON shape for each of the four with `03-runner-adapter-and-job-taxonomy.md` (parallel-safe per the README's dependency table, but the two need to agree on the wire shape before either is done, since `03`'s Runner decodes exactly what this adapter encodes).
3. Implement `ClaimAndUpdateScheduleRun(ctx, id, expectedNext, lastRun, nextRun) (bool, error)` — the compare-and-set: `UPDATE agent_schedules SET last_run=?, next_run=? WHERE id=? AND next_run=?`, returning `RowsAffected() == 1`. This is the one method where getting the exact CAS semantics right matters most — a regression test must prove a losing concurrent claim attempt returns `false, nil`, not an error and not a silent double-claim.
4. Implement `SetScheduleNextRun(ctx, id, nextRun) error` and `DisableSchedule(ctx, id) error` — straightforward single-row updates.
5. Compute `next_run` for a `cron`-kind schedule from `schedule_spec` using `go-scheduler.NextRun(expr, from)` (`libs/go-scheduler/scheduler.go:85` — reuse the library's own helper, don't hand-roll cron-next-occurrence logic a second time) rather than continuing to use `wakeScheduleDue`'s own per-call check (retired by `05-engine-wiring-and-full-replace.md`, but this adapter is what makes that retirement possible — don't leave a second independent cron-math implementation alive).

## Done means

- The adapter satisfies `go-scheduler.Store` (compile-time assertion, `var _ gosched.Store = (*yourAdapterType)(nil)` or equivalent).
- A regression test against a real (not in-memory-only, given the single-connection-pool safety argument this task rests on) `*store.Store` instance proves: two concurrent goroutines racing `ClaimAndUpdateScheduleRun` on the same due schedule — exactly one wins, the other sees `false, nil`.
- A regression test proves `ListDueSchedules` correctly excludes disabled schedules and schedules whose `next_run` is in the future, and correctly caps at `limit`.
- A regression test proves a `one_shot` schedule's `NextRun`/next-run computation and a `cron` schedule's (via `gosched.NextRun`) both round-trip correctly through the adapter.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Your package-location call (step 1) and your embedding-vs-separate-wrapper call (Context) are documented in this file's Work Log.

## Work log

Not started.

## Review notes

<!-- Reviewer fills in. -->
