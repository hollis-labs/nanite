# LoopRun schema — `loop_runs` table, Go types, store CRUD

**Phase:** 1 — Schema & storage foundation (`TASKS/loops`)
**Status:** reviewed
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

**Migration number.** Used `138` (since renumbered to `141`; see
`TASKS/loops/HANDOFF.md`'s 2026-08-22 renumbering note), per this task's explicit
dispatch-time assignment (not the `140` provisionally claimed in "What to do" §1, and not
a self-re-verified number) -- the orchestrator's own kickoff message fixed `138` to avoid
a repeat of Wave 1's `01`/`06` migration-number collision, since a sibling worker was
concurrently claiming `137` (now `140`) for task `02` off the same base. Confirmed no
`137_*`/`138_*` file existed in this worktree at the time (now `140_*`/`141_*`) before
writing `141_loop_runs.sql`; the
latest pre-existing migration visible here was `139_workflow_run_steps_loop_kind.sql`.

**Migration** (`internal/store/migrations/141_loop_runs.sql`) -- `loop_runs` table
matching the task's illustrative DDL exactly (own `id TEXT PRIMARY KEY`, never keyed by
or aliased to `workflow_runs.id`, per 21-loops.md's Decision 1 and this task's explicit
instruction). `goal_id NOT NULL REFERENCES goals(id)` (FK-enforced, confirmed by a test).
Brand-new table -> plain transactional `CREATE TABLE`, matching `128_teams.sql`/
`138_goals.sql`'s precedent (no rebuild dance needed, unlike 130/133/139's CHECK-widening
migrations against an already-populated table). Tested by running the full migration
chain against a real copy of `~/.local/share/nanite/workspaces/default/backups/
main.db.pre-execution-backup-20260818-132726` (the ~38MB populated backup) via a
throwaway `cmd/tmp_migration_check141/main.go` (deleted after verification, never
committed) that called `store.New` (runs every embedded migration via goose) and then
exercised `CreateGoal`/`CreateLoopRun`/`GetLoopRun`/`SetBudget`/`UpdateLoopRunStatus`/
`DeleteLoopRun`/`DeleteGoal` against that real DB copy -- all succeeded.

**`on_exhausted` placement.** Encoded inside `budget_json` (not a separate column), per
the task's own instruction and this planning session's decision (Context section):
`on_exhausted` is one policy knob among several already JSON-blobbed together
(`max_iterations`/`max_failures`/`max_runtime_seconds`/`max_no_progress_iterations`), and
splitting only this one field into its own column while leaving the rest blobbed would be
an arbitrary asymmetry. `Budget.OnExhausted` defaults to `LoopRunOnExhaustedEscalate`
inside `SetBudget` when left empty (this session's "default escalate" decision), then
`validateBudget` enforces the two-value enum + non-negative-int invariants before
encoding -- an invalid `OnExhausted` (or a negative `max_*` field) is rejected by
`SetBudget` before it ever reaches `BudgetJSON`, let alone the DB.

**Go types + store CRUD** (`internal/store/loop_runs.go`), mirroring `goals.go`'s and
`agent_schedules.go`'s conventions exactly:
- `LoopRun` struct with all schema columns; `Status` defaults to `LoopRunStatusRunning`
  and `BudgetJSON`/`ContinuationPolicyJSON` default to `"{}"` at `CreateLoopRun` (same
  "SQLite column DEFAULT never actually applies because every value is passed explicitly"
  trade-off `goals.go`/`agent_schedules.go` already document).
- `Budget` struct exactly as specified (`MaxIterations`, `MaxFailures`,
  `MaxRuntimeSeconds`, `MaxNoProgressIterations` all `int`; `OnExhausted string`), decoded/
  encoded via the `Budget()`/`SetBudget()` typed accessor pair (mirrors `goals.go`'s
  `DesiredState()`/`SetDesiredState()`-style convention). `validateBudget` lives at
  package scope and is invoked from `SetBudget`, not from `CreateLoopRun` -- documented
  in-code why: Budget is the first JSON sub-structure in this package carrying a real
  enum, so its own accessor is the validation gate, the same way `validateGoalStatus`
  gates a top-level column rather than a JSON blob.
- `ContinuationPolicyJSON` left as a plain-string placeholder field, no typed accessor --
  task `07`'s job, per the task file's own instruction and the same "don't guess a shape
  another task owns" discipline `TASKS/teams/01` set for `authority_json`/`routing_json`.
- `CreateLoopRun`, `GetLoopRun`, `ListLoopRuns(filter LoopRunFilter)` (filters by
  `GoalID` and/or `Statuses []string`, the latter via a `status IN (...)` clause so task
  `10`'s one-active-run-per-goal check can pass a status *set*, not just one value),
  `UpdateLoopRunStatus(ctx, id, status, completedAt *time.Time)` (narrow updater mirroring
  `agent_schedules.go`'s `UpdateAgentScheduleStatus`; `completedAt` is nil-means-untouched
  rather than internally derived from a terminal-status set, since the caller -- the
  continuation policy engine, task `08` -- already knows whether a transition is
  terminal), `BumpLoopRunIteration` (narrow, `+1`, mirrors `BumpAgentScheduleFireCount`),
  `UpdateLoopRunNoProgressStreak(ctx, id, streak int)` (narrow, a direct *set* not an
  increment, per the task's own description of task `08`'s call pattern), `DeleteLoopRun`.
  All narrow updaters also bump `updated_at` to now, since that column exists in the
  schema and nothing else was going to keep it meaningful.
- `ErrLoopRunNotFound` sentinel, `LoopRunStatus*` constants + `validateLoopRunStatus`
  (enum-membership only, matching `validateGoalStatus`'s explicit "real transition-
  legality enforcement is a later task's job" scope fence), `LoopRunOnExhausted*`
  constants.
- Added one small addition beyond the task's literal list: an exported
  `LoopRunActiveStatuses = []string{running, waiting_on_gate, waiting_on_escalation}` var,
  so task `10`'s launcher (and anything else needing "is this LoopRun still in play")
  shares one definition of "active" instead of each caller re-declaring the same three-
  value list the task's own Context section names verbatim. Not an enforcement mechanism
  by itself -- documented as such in its doc comment.

**Tests** (`internal/store/loop_runs_test.go`, new): `TestLoopRun_RoundTrip` (full CRUD +
status/iteration/streak narrow updaters + completedAt handling),
`TestLoopRun_BudgetOnExhaustedBothValues` (both enum values round-trip, plus the
"unset defaults to escalate" behavior), `TestLoopRun_BudgetValidation` (invalid
`OnExhausted` and each negative `max_*` field rejected by `SetBudget` before touching the
DB), `TestLoopRun_ListLoopRuns_GoalAndActiveStatusFilter` (the task's explicit "Done
means" regression test -- multiple `LoopRun`s across two goals and all six statuses;
`ListLoopRuns(GoalID, LoopRunActiveStatuses)` returns exactly the three active rows for
the targeted goal), `TestLoopRun_DefaultsAndNotFound`, `TestLoopRun_StatusValidation`,
`TestLoopRun_StatusCheckConstraint` (raw INSERT bypassing Go validation still rejected by
the DB CHECK), `TestLoopRun_GoalFKEnforced`.

**Deviation from plan:** none of substance. The only departure from the task file's own
"What to do" §1 is the migration number (`138`, since renumbered to `141`, instead of the
provisionally-claimed `140`), which was an explicit, intentional override from the dispatching orchestrator to
avoid a cross-worktree collision with a concurrently-running sibling task, not a
deviation I chose.

**Verification:** `go build ./cmd/nanite/` -- pass. `go vet ./...` -- one pre-existing,
unrelated finding in `internal/service/container.go` (`stopReaper`/`stopRuntimeReaper`
possible-context-leak lint), confirmed via `git status`/`git diff` to be untouched by this
task's changes (only the three new files above are untracked in this worktree) and
therefore pre-existing; `go vet ./internal/store/...` alone is clean. `go test ./...` --
all packages pass, including the full `internal/store` suite and the new `loop_runs_test.go`
tests. Migration `141` also independently verified by running the full embedded migration
chain against a real, populated backup DB copy
(`~/.local/share/nanite/workspaces/default/backups/main.db.pre-execution-backup-20260818-132726`)
and round-tripping a `Goal`/`LoopRun` pair through it end to end.

No engine, launcher, or continuation-policy logic was added -- this task is storage-only,
per its own "Done means" scope fence.

## Review notes

Prior reviewer's substantive findings on this task's own diff (loop_runs schema, enum
vocabularies, store CRUD, test coverage) stand — no re-review of that content performed
here. This pass confirmed the merged-tree `makeTestGoal` duplicate was removed from this
file's `loop_runs_test.go` (commit `d6da6efe`; the surviving definition lives in
`goal_evidence_test.go` per task 02): `go vet ./internal/store/...` clean,
`go test -count=1 ./internal/store/...` passes (15.5s, non-cached), and
`go test -count=1 ./...` passes across all 92 packages (non-cached, exit-code verified, no
`FAIL`/`panic` in output).
