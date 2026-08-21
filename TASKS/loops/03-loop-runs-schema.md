# LoopRun schema — `loop_runs` table, Go types, store CRUD

**Phase:** 1 — Schema & storage foundation (`TASKS/loops`)
**Status:** not-started
**Depends on:** `01-goals-schema.md` (`goal_id NOT NULL` FK)
**Touches:** `internal/store/migrations/` (new migration), `internal/store/loop_runs.go`
(new — `LoopRun` struct, CRUD).

## Context

Implements `docs/engineering/architecture/21-loops.md`'s Decision 1: `LoopRun` is a **new
peer entity to `WorkflowRun`, not a `WorkflowRun` itself** — the opposite of Teams'
`TeamRun`-IS-a-`WorkflowRun` collapse. Quoted directly, the reason: *"A Loop's defining
property — the plan can change shape between iterations (REPLAN/REARCHITECT), and
iterations can run genuinely different executors — is exactly what `agentworkflow`'s
DAG-only scope deliberately excludes. Collapsing Loop into WorkflowRun the way Team did
would either reopen that scope boundary... or corrupt what WorkflowRun means."*
`LoopRun` orchestrates a *sequence* of ordinary `WorkflowRun`s (task `04`'s
`loop_run_iterations` records each one) rather than owning a second execution engine.

**This planning session's own decisions this table encodes** (full reasoning in the
README's "What this session decided" section):
- **`on_exhausted ∈ {"escalate","fail"}`**, default `"escalate"` — narrower than
  Scheduling's `retry`/`disable`/`notify` (retry doesn't apply once the *total* budget is
  exhausted; disable/notify are Schedule-specific).
- **One active `LoopRun` per `goal_id` at a time** — enforced by task `10`'s launcher at
  launch time (an application-level check, not a DB constraint — a partial unique index on
  SQLite would need `WHERE status IN ('running','waiting_on_gate','waiting_on_escalation')`
  which is valid SQLite but brittle against future status additions; a Go-layer check in
  `LoopLauncher.Launch` is simpler and this task's own `ListLoopRuns` supports it directly).

## What to do

1. **New migration** — re-verify the actual next-available migration number at dispatch
   time; this task provisionally claims `140`. Illustrative DDL, matching the design doc's
   own illustrative schema section closely:
   ```sql
   CREATE TABLE IF NOT EXISTS loop_runs (
       id                       TEXT PRIMARY KEY,
       goal_id                  TEXT NOT NULL REFERENCES goals(id),
       definition_name          TEXT NOT NULL,
       status                   TEXT NOT NULL DEFAULT 'running'
                                CHECK (status IN ('running','completed','failed','cancelled',
                                       'waiting_on_gate','waiting_on_escalation')),
       current_iteration        INTEGER NOT NULL DEFAULT 0,
       budget_json              TEXT NOT NULL DEFAULT '{}',
       continuation_policy_json TEXT NOT NULL DEFAULT '{}',
       no_progress_streak       INTEGER NOT NULL DEFAULT 0,
       started_at               TEXT NOT NULL DEFAULT (datetime('now')),
       updated_at               TEXT NOT NULL DEFAULT (datetime('now')),
       completed_at             TEXT
   );
   CREATE INDEX idx_loop_runs_goal ON loop_runs(goal_id, status);
   CREATE INDEX idx_loop_runs_status ON loop_runs(status);
   ```
   Note the design doc's illustrative `budget` shape (§ "Illustrative shape"):
   `{ max_iterations, max_failures, max_no_progress_iterations }` (`max_runtime` also named
   in the schema ledger section) — encode `on_exhausted` as a field *inside*
   `budget_json`, not a separate column, since it's one policy knob among several already
   JSON-blobbed together; document your call if you split it out instead.

2. **Go types + store CRUD**, `internal/store/loop_runs.go`, same conventions as task `01`:
   - `LoopRun` struct mirroring the columns.
   - `Budget` struct (`MaxIterations int`, `MaxFailures int`, `MaxRuntimeSeconds int`,
     `MaxNoProgressIterations int`, `OnExhausted string` — `"escalate"|"fail"`) decoded from
     `budget_json` via typed accessors, with a `validateBudget` (enum check on
     `OnExhausted`, non-negative ints).
   - `continuation_policy_json` — leave as a plain-string placeholder (or minimally typed)
     for now; task `07` defines and consumes its real shape without needing this task
     revisited, same "don't guess a shape another task owns" discipline `TASKS/teams/01`
     applied to `authority_json`/`routing_json`.
   - `CreateLoopRun`, `GetLoopRun(id)`, `ListLoopRuns(filter ...)` (at minimum: filter by
     `goal_id`, filter by `status` — task `10`'s one-active-run-per-goal check calls
     `ListLoopRuns(goalID, statusIn: {running, waiting_on_gate, waiting_on_escalation})`),
     `UpdateLoopRunStatus(id, status, completedAt *time)` (narrow updater, mirrors
     `agent_schedules.go`'s pattern), `BumpLoopRunIteration(id)` (increments
     `current_iteration`, narrow), `UpdateLoopRunNoProgressStreak(id, streak int)` (narrow —
     task `08` calls this after every iteration's evaluation), `DeleteLoopRun`.
   - `ErrLoopRunNotFound` sentinel.

## Done means

- Migration applies cleanly against a real backup copy of the database.
- `LoopRun` CRUD round-trips correctly, including `Budget`'s JSON sub-structure with
  `OnExhausted` set to each of its two valid values, and rejects an invalid `OnExhausted`
  via `validateBudget`.
- `ListLoopRuns` filtered by `goal_id` + an "active" status set returns the expected subset
  in a regression test with multiple `LoopRun`s across different goals and statuses.
- This task is storage-only — no engine or launcher logic (tasks `07`/`08`/`10`).
  `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
