# Schema — collapse `schedule_kind`, add retry-policy columns, new `schedule_runs` table

**Phase:** 1 — Core engine (`TASKS/scheduling`)
**Status:** not-started
**Depends on:** none
**Touches:** `internal/store/migrations/` (new migration, table-rebuild of `agent_schedules` + a new `schedule_runs` table), `internal/store/agent_schedules.go` (`ScheduleKind*` constants, `AgentSchedule` struct, `GetDueSchedules`/`scheduleFires` removal), `internal/store/agent_state_store.go` (`AgentStateStore` interface — drop `GetDueSchedules`), `internal/service/managed_durable_configs.go` (doc-comment listing all five `Kind` values, `internal/service/managed_durable_configs.go:51-52`).

## Context

`docs/engineering/architecture/12-scheduling.md`'s "Full replace, not dual-run" and "Retry, backoff, and `on_fail` policy" sections are the design. Confirmed directly against this checkout (not assumed) before writing this task:

- `internal/store/migrations/071_agent_schedules.sql` is the live schema: `schedule_kind TEXT NOT NULL CHECK (schedule_kind IN ('every_n_ticks','on_tick','cron','one_shot','on_event'))`. Only `cron`/`one_shot` have a live production path (`durable_wake.go`'s `wakeScheduleDue`).
- **`agent_schedules` has no `next_run` column today** — confirmed directly against `071_agent_schedules.sql`'s full column list (`id, agent_id, session_id, name, schedule_kind, schedule_spec, body, priority, status, expires_at, fired_count, last_fired_at, created_at, created_by`). `wakeScheduleDue` currently recomputes due-ness from `schedule_spec` on every poll rather than reading a persisted next-fire time. `go-scheduler`'s entire CAS-claim mechanism (`libs/go-scheduler/scheduler.go:52-56`, `ClaimAndUpdateScheduleRun(ctx, id, expectedNext, lastRun, nextRun)`) depends on a persisted `next_run` to compare-and-set against — this is not optional plumbing, it's the correctness mechanism itself. This task adds it; `02-store-adapter.md` consumes it, it does not also need to add it.
- **`agent_schedules` also has no column encoding which of the four job types (`docs/engineering/architecture/12-scheduling.md`'s "The Runner adapter and job taxonomy" table) a row represents, or a generic structured payload for the three new types.** `body` (the existing column) is already the payload for `durable_agent_wake` specifically — "the instruction text delivered as the woken session's first user turn," per `managed_durable_configs.go`'s own doc comment. `agent_workflow_run` (workflow name + params), `command_run` (command name + args), and `reflex_dispatch` (which reflex) each need their own structured config with no existing column to reuse. This task adds that representation; `03-runner-adapter-and-job-taxonomy.md` owns decoding it per job type.
- `GetDueSchedules` (`internal/store/agent_schedules.go:233`, `scheduleFires`'s `every_n_ticks`/`on_tick`/`on_event` branches at `:281`/`:291`/`:321`) has **zero production callers** — confirmed by grep, only exercised by `internal/store/agent_schedules_test.go`. It's part of the `AgentStateStore` interface's contract (`internal/store/agent_state_store.go:66-68`, `var _ AgentStateStore = (*Store)(nil)` at `:74`) — the interface's own comment names it as a seam for "the per-agent-file backend," which doesn't exist yet; removing this one method from the interface doesn't touch anything else in it.
- `internal/service/managed_durable_configs.go:51-52`'s doc comment on `ManagedDurableAgentSchedule.Kind` lists all five constants as valid — needs correcting to name only `store.ScheduleKindCron`/`store.ScheduleKindOneShot`, and if any live validation code accepts the other three (grep for it — the design doc's own review found none, but re-verify), tighten it to reject them.

**Explicit recommendation, not a mandate — document your call in this file's Work Log:** rebuild `agent_schedules` in place (same table-rebuild pattern `TASKS/reflex-taxonomy/01-taxonomy-schema-foundation.md` used for its own CHECK-constraint change — SQLite can't `ALTER` a `CHECK` in place) rather than leaving the three dead values in the constraint "for now." The design doc is explicit that keeping the schema slots would recreate the exact live-looking-but-isn't-really-live ambiguity `CW-20260819-0006` was filed to resolve — don't soften this to a keep-for-compat call without flagging it back to the operator first.

## What to do

1. **Rebuild `agent_schedules`** (table-rebuild migration, same pattern as `internal/store/migrations/119_agent_reflex_dispatch_to_agent.sql`/`115_agent_reflex_opt_out.sql` — `NO TRANSACTION` + explicit `PRAGMA foreign_keys`/`BEGIN`/`END` toggle):
   - `schedule_kind` CHECK narrowed to `IN ('cron','one_shot')` only.
   - Add `max_retries INTEGER NOT NULL DEFAULT 3` (illustrative default — the design doc doesn't name a specific value; document your choice) and `on_fail TEXT NOT NULL DEFAULT 'retry' CHECK (on_fail IN ('retry','disable','notify'))`, per `docs/engineering/architecture/12-scheduling.md`'s "Retry, backoff, and `on_fail` policy" section (`retry` "doesn't apply here by definition" once `max_retries` is exhausted — that's the policy the Runner applies at exhaustion, not a fourth do-nothing state; document how you reconcile the column's own default value with that framing).
   - Add `next_run TEXT` (nullable — `NULL` means unscheduled/never-computed, matching `go-scheduler`'s own "zero `NextRun` means unscheduled and is skipped" convention, `libs/go-scheduler/scheduler.go:28`) — the persisted next-fire time the CAS claim compares against (see Context above). Backfill it for every existing enabled row as part of this migration: for `cron`-kind rows, compute via the same cron-parsing logic `go-scheduler.NextRun`/`robfig/cron` already provides (from `now`, at migration time); for `one_shot`-kind rows, backfill from whatever the row's existing due-time representation is (`schedule_spec`, if that's where a one-shot's target time lives — confirm the current one-shot encoding by reading `wakeScheduleDue` before assuming). Rows already fired/expired (`status='expired'`) can backfill `next_run` to `NULL` rather than computing a stale value. Repurpose the existing `last_fired_at` column as the adapter's `LastRun` source (`02-store-adapter.md` reads it directly) — do not add a redundant `last_run` column alongside it.
   - Add `job_type TEXT NOT NULL DEFAULT 'durable_agent_wake' CHECK (job_type IN ('durable_agent_wake','agent_workflow_run','command_run','reflex_dispatch'))` and `job_payload TEXT NOT NULL DEFAULT '{}'` (JSON, generic structured config for the three new job types — see Context). Backfill every existing row's `job_type` to `'durable_agent_wake'` (the only value in live use today) and leave `job_payload` at its default `'{}'` — `03-runner-adapter-and-job-taxonomy.md`'s Store-adapter-side conversion for `durable_agent_wake` reads `body` directly rather than requiring a payload backfill, so an empty `job_payload` on every existing row is correct, not a gap. Keep `body` as-is (not renamed, not removed) — it stays the live `durable_agent_wake` payload field; `job_payload` is additive for the other three job types only.
   - **No existing rows should be dropped by the CHECK narrowing** — confirm at migration-write time (query the real backup, per "Done means" below) that no live row currently holds `every_n_ticks`/`on_tick`/`on_event`; the design doc's own review already found the one production row is YAML-sync-authored and already `cron`/`one_shot`, but verify directly against a real backup rather than trusting that secondhand.
2. **New table `schedule_runs`**, illustrative shape per the design doc (architecture-level agreement, not migration-ready DDL):
   ```sql
   CREATE TABLE schedule_runs (
       id              TEXT PRIMARY KEY,
       schedule_id     TEXT NOT NULL REFERENCES agent_schedules(id),
       run_id          TEXT NOT NULL,
       fired_at        TEXT NOT NULL,
       status          TEXT NOT NULL CHECK (status IN ('pending','succeeded','failed','exhausted')),
       attempt_count   INTEGER NOT NULL DEFAULT 0,
       last_error      TEXT,
       next_attempt_at TEXT
   );
   CREATE UNIQUE INDEX idx_schedule_runs_run_id ON schedule_runs(run_id);
   CREATE INDEX idx_schedule_runs_schedule_id ON schedule_runs(schedule_id, fired_at DESC);
   ```
   `run_id` is `go-scheduler`'s `Job.RunID` (`libs/go-scheduler/scheduler.go:39` — "engine-generated identifier, unique per fire") — one `schedule_runs` row per firing, which `04-retry-backoff-on-fail-policy.md` looks up by `run_id` on every `Runner.Enqueue` attempt for that firing. Document your `status` vocabulary choice if you deviate from the four above (`04`'s Runner logic depends on whatever states you land on here — coordinate, don't just pick independently if `04` is already in flight).
3. **New migration number**: `126_schedule_runs_and_retry_policy.sql` (latest existing at this task's authoring time is `125_reflex_action_kind_provenance_allow.sql`). **Cross-batch numbering collision, real, not hypothetical**: `TASKS/harness-reactive-self-tools/02-reactive-layer-schema.md` independently claims `126` too, against the same baseline. Whichever of the two lands second must re-list `internal/store/migrations/` and renumber to the actual next-available number before writing its migration file.
4. **Go-side**: remove `ScheduleKindEveryNTicks`/`ScheduleKindOnTick`/`ScheduleKindOnEvent` constants and `GetDueSchedules`/`scheduleFires`'s three now-dead branches (or the whole function, if nothing else calls it after this — confirm via grep, since `03-runner-adapter-and-job-taxonomy.md`'s Store adapter uses `go-scheduler.Store.ListDueSchedules`, a different method entirely, not this one). Remove `GetDueSchedules` from the `AgentStateStore` interface (`internal/store/agent_state_store.go:66-68`) and its doc comment. Add `MaxRetries int64`/`OnFail string`/`NextRun string`/`JobType string`/`JobPayload string` fields to the `AgentSchedule` struct (`internal/store/agent_schedules.go:38-53`), wired into `agentScheduleColumns`, `scanAgentSchedule`, `InsertAgentSchedule`, and any `Update*` path that should be able to change them.
5. Correct `internal/service/managed_durable_configs.go:51-52`'s doc comment to name only the two live `Kind` values.

## Done means

- Migration `126_schedule_runs_and_retry_policy.sql` (or its renumbered equivalent, per step 3's collision note) applies cleanly against a **real backup copy** of the database, not just an empty fixture, per `EXECUTION-PROCESS.md`'s schema-migration testing requirement — including the explicit check that no real row is dropped or corrupted by the `schedule_kind` CHECK narrowing.
- `agent_schedules` has exactly two live `schedule_kind` values possible going forward; `schedule_runs` exists with the shape above (or your documented variant).
- `agent_schedules.next_run` exists, correctly backfilled for every real pre-existing enabled row (spot-check against a real backup — no row silently loses its due-ness).
- `AgentSchedule`'s new `MaxRetries`/`OnFail`/`NextRun`/`JobType`/`JobPayload` fields round-trip correctly in a regression test.
- `GetDueSchedules`/`scheduleFires`'s three dead branches (and the `AgentStateStore` interface method, if step 4's grep confirms it's safe) are removed — `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass with no dangling reference.
- Your table-rebuild-vs-keep-for-compat call (Context), your `max_retries` default value, your `on_fail` vocabulary, and your `schedule_runs.status` vocabulary are documented in this file's Work Log.

## Work log

Not started.

## Review notes

<!-- Reviewer fills in. -->
