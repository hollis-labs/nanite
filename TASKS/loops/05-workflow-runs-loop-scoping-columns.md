# `workflow_runs` loop-scoping columns — `loop_run_id`, `loop_iteration`

**Phase:** 1 — Schema & storage foundation (`TASKS/loops`)
**Status:** implemented
**Depends on:** `03-loop-runs-schema.md` (`loop_run_id`'s FK target must exist before this
migration runs)
**Touches:** `internal/store/migrations/` (new migration), `internal/store/workflow_runs.go`
(or wherever `WorkflowRun` reads/writes live — add the two fields to the struct and its
scan/insert paths).

## Context

Implements `docs/engineering/architecture/21-loops.md`'s explicit schema-ledger line:
`workflow_runs.loop_run_id NULL` / `workflow_runs.loop_iteration NULL` — *"run-scoping FK,
same pattern as migration 131's `agent_reflexes` scoping."* Confirmed this session by
reading `internal/store/migrations/131_agent_reflexes_workflow_run_scoping.sql` directly:
a plain nullable `ALTER TABLE agent_reflexes ADD COLUMN workflow_run_id TEXT REFERENCES
workflow_runs(id);` + one index, no `PRAGMA foreign_keys`/rename-recreate-copy rebuild —
because the column carries no `CHECK` constraint, SQLite's `ADD COLUMN` handles it directly.
This task's two new columns are the same shape (a nullable FK, a nullable plain integer),
so the same lightweight migration form applies, not `128`/`132`'s "define a whole new
table" form or `130`/`133`'s "widen an existing `CHECK`" rebuild form.

**Purpose:** every `WorkflowRun` launched as one loop iteration (by `LoopEngine`, task `08`)
gets tagged with which `LoopRun` and which iteration number it belongs to — this is how a
`WorkflowRun` row answers "am I part of a loop, and which one/which iteration" without a
join through `loop_run_iterations` (which points the other direction: `LoopRun` → its
`WorkflowRun`s). Both directions are kept because they serve different queries: this
column answers "given a `WorkflowRun`, what loop is it in" (useful for A2A/UI surfacing a
running workflow's loop context); `loop_run_iterations` answers "given a `LoopRun`, list
its iterations in order."

## What to do

1. **New migration** — re-verify the actual next-available migration number at dispatch
   time; this task provisionally claims `142` (after task `04`'s `141`, since this migration
   must run after `03`'s `loop_runs` table exists — re-sequence if `03`/`04` land in a
   different relative order than assumed). Exact DDL, mirroring `131` precisely:
   ```sql
   ALTER TABLE workflow_runs ADD COLUMN loop_run_id TEXT REFERENCES loop_runs(id);
   ALTER TABLE workflow_runs ADD COLUMN loop_iteration INTEGER;
   CREATE INDEX idx_workflow_runs_loop_run ON workflow_runs(loop_run_id, loop_iteration);
   ```

2. **Go struct + read/write paths** — find `WorkflowRun`'s Go struct (in `internal/store/`,
   likely alongside `workflow_run_steps` handling — grep `type WorkflowRun struct`) and add
   `LoopRunID *string` / `LoopIteration *int` fields, wired through whatever scan/insert
   function(s) currently populate that struct from a DB row. This task does **not** set
   these fields anywhere yet (no engine calls it) — that's task `08`'s job when it launches
   an iteration's `WorkflowRun` via `WorkflowLauncher.Launch`. Confirm `WorkflowLaunchRequest`
   (or whatever request type `Launch` takes, `internal/service/workflow_launch.go`) has a
   place to pass these two values through to the created row, or note explicitly if task `08`
   needs to set them via a follow-up `UpdateWorkflowRunLoopScope`-style call after `Launch`
   returns instead — document whichever is true, don't assume.

## Done means

- Migration applies cleanly against a real backup copy of the database, including one that
  already has real `workflow_runs` rows (columns must default to `NULL`, no data loss/change
  to existing rows).
- `WorkflowRun` struct round-trips both new fields (a regression test: create a plain
  `WorkflowRun` with both `NULL`, then a second with both set, read both back correctly).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Confirmed and documented in the Work Log: which real call site task `08` will use to set
  these two fields (either `Launch`'s request struct, or a follow-up updater this task adds
  if `Launch`'s signature can't reasonably be extended without touching every other caller —
  if the latter, add the narrow updater here so task `08` doesn't have to revisit this file).

## Work log

**Migration number:** used `140` exactly, per explicit dispatch instruction (task `04` was
assigned `139` concurrently in a sibling isolated worktree; no re-verify-and-pick-my-own).
Confirmed no `139_*`/`140_*` migration existed in this worktree before creating it — highest
on disk was `138_loop_runs.sql`. New file:
`internal/store/migrations/140_workflow_runs_loop_scoping.sql`. Exact DDL as specified in
"What to do" §1 (plain nullable `ADD COLUMN`s + one composite index,
`CREATE INDEX IF NOT EXISTS` matching 131's own style), mirroring migration
`131_agent_reflexes_workflow_run_scoping.sql` precisely — no table rebuild, since neither new
column carries a `CHECK` constraint. `Down` drops the index then both columns (131's own
symmetric-Down precedent).

**Go struct:** the real name is `WorkflowRunRow`, not `WorkflowRun` (the task file's own grep
hint didn't match — confirmed by grepping `WorkflowRun struct` across the whole tree, zero
hits; the actual type lives in `internal/store/workflow_runs.go`). Added
`LoopRunID *string` / `LoopIteration *int` fields exactly as the task's "What to do" §2
specifies (pointer, not the plain-string-with-`nullIfEmpty` convention `agent_reflexes.go`/
`goal_evidence.go` use for their own nullable string FK columns — `LoopIteration` in
particular needs a pointer regardless, since `0` is a legitimate iteration number and can't
double as an "unset" sentinel; `sessions.go`'s `Depth *int`/`HaltStatus`'s `*string` fields
are the established in-repo precedent for this exact pointer shape, scanned via
`sql.NullString`/`sql.NullInt64` intermediaries in `scanWorkflowRunRow`, matching
`sessions.go`'s `hydrateSessionOptionalFields` convention). `workflowRunColumns` extended;
`CreateWorkflowRun`'s INSERT threads `row.LoopRunID`/`row.LoopIteration` straight through
(Go's `database/sql` default converter maps a nil pointer to `NULL` and a non-nil pointer to
its dereferenced value automatically — no `nullIfEmpty`-style helper needed on the write
side).

**`WorkflowLaunchRequest` finding (task's "What to do" §2, explicitly asked to be
documented):** confirmed by reading `internal/service/workflow_launch.go` and
`internal/service/workflow_engine.go` directly — `WorkflowLaunchRequest`/`Launch` is **not**
a natural place to pass `loop_run_id`/`loop_iteration` through, and a follow-up narrow
updater is the honest answer, not an assumption. `Launch` never itself calls
`CreateWorkflowRun`; it calls `engine.Run(ctx, wf, agentworkflow.WorkflowInput{...}, l.exec)`
against the generic `agentworkflow.WorkflowEngine` interface, and it's `BuiltinWorkflowEngine.
Run` (`internal/service/workflow_engine.go:96`) — several layers down, past an
engine-agnostic interface any external engine (LangGraph/CrewAI per `EngineBuiltin`'s own
doc) also implements — that actually constructs the `WorkflowRunRow` and calls
`CreateWorkflowRun`. `agentworkflow.WorkflowInput` (the shared, engine-agnostic input type
every implementation consumes) carries no loop-scoping concept, and extending it just to
plumb two loop-specific fields through every `WorkflowEngine` implementation is a
categorically bigger change than this task's scope, not a narrow field addition to
`WorkflowLaunchRequest` alone. Added `Store.UpdateWorkflowRunLoopScope(id, loopRunID string,
loopIteration int) error` to `internal/store/workflow_runs.go` instead — a narrow updater
(mirrors `SetWorkflowRunStatus`'s shape/error convention exactly, including
`ErrWorkflowRunNotFound` on a no-op update) task `08`'s `LoopEngine` is expected to call
immediately after `WorkflowLauncher.Launch` returns its `WorkflowLaunchResult.RunID`, per
this task's own "Done means" fallback instruction. Documented directly in
`CreateWorkflowRun`'s and `UpdateWorkflowRunLoopScope`'s doc comments so task `08` doesn't
have to re-derive this.

**Regression test:** `internal/store/workflow_runs_loop_scoping_test.go`,
`TestWorkflowRun_LoopScope_RoundTrips` — creates a real `Goal`/`LoopRun` (satisfying
`loop_runs.goal_id NOT NULL`), then: (1) a plain `WorkflowRunRow` with both new fields left
nil, confirming they read back as `nil`/NULL; (2) a `WorkflowRunRow` with both fields set at
insert time, confirming round-trip; (3) `UpdateWorkflowRunLoopScope` against the plain row
(the actual documented task-08 call shape), confirming it stamps correctly; (4) the
not-found and empty-`loopRunID` error paths.

**Deviation — a pre-existing test broke and was fixed, not the production schema/code:**
`TestRealBackupWorkflowRunStepsSurviveLoopKindMigration`
(`internal/store/migration_136_workflow_run_steps_loop_kind_test.go`, landed by task `06`)
does a `goose DownTo(134)` / synthetic-flex-row-insert / `UpTo(136)` dance directly against a
real backup copy to test migration 136's table-rebuild in isolation. It calls the shared
`CreateWorkflowRun` helper to create the flex row's parent `workflow_runs` row *after*
`DownTo(134)` — which, before this task, always worked because no migration between 001 and
136 had ever added a column to `workflow_runs` itself (only `workflow_run_steps` was
rebuilt). This task's migration 140 is the first to add columns to `workflow_runs`, and
`CreateWorkflowRun` (correctly, per this task's own scope) now unconditionally references
them — breaking that call once run against a schema rolled back below 140. Confirmed via a
full `go test ./...` run that this was the *only* casualty. Fixed by moving that one
`CreateWorkflowRun` call to *before* `DownTo(134)` (the row only needs to exist as an FK
target for the subsequent `workflow_run_steps` insert; `ALTER TABLE ... DROP COLUMN`, run by
`DownTo`'s reversal of migration 140, removes only the two columns, not the row) and
replacing the final `provider.UpTo(ctx, 136)` with `provider.Up(ctx)` so the later
"new capability" `CreateWorkflowRun` call (which needs migration 140's columns) also
succeeds — neither change weakens the file's own 136-rebuild-specific assertions, since
migrations 137-140 never touch `workflow_run_steps`. This is a real, load-bearing side
effect of this task's own schema change on another task's already-landed test, not a
production-code compromise.

**Real-backup verification (beyond the two backup-file test suites already in the repo,
which both passed unmodified/as-fixed):** copied
`~/.local/share/nanite/workspaces/default/backups/main.db.pre-execution-backup-20260818-132726`
to a scratch path and ran the real `store.New()` migration path (via a throwaway `cmd/`
program, deleted afterward — `git status` confirmed clean before finishing) against it. That
backup has exactly one real, pre-existing `workflow_runs` row (`status='failed'`, predating
even the `waiting_on_flex` status value in its on-disk `CHECK`). Migration 140 applied
cleanly; the row survived unchanged with `loop_run_id`/`loop_iteration` both `NULL`; and
calling `UpdateWorkflowRunLoopScope` against that real row with a nonexistent `loop_run_id`
correctly failed with a real SQLite `FOREIGN KEY constraint failed` error, confirming the new
column's `REFERENCES loop_runs(id)` is actually enforced, not just declared.

**Verification:** `go build ./cmd/nanite/` clean (exit 0, checked directly). `go vet
./internal/store/...` clean (exit 0). `go vet ./...` reports the same two pre-existing,
unrelated `internal/service/container.go` findings (`stopReaper`/`stopRuntimeReaper`
possible-context-leak) every prior batch in `TASKS/ESCALATIONS.md` has already hit and
logged — confirmed via `git diff HEAD -- internal/service/container.go` (zero output, file
untouched by this task), not `git stash`, per this dispatch's explicit prohibition. `go test
./...` clean (exit 0, checked directly with no pipe to `tail`/other commands — output
redirected to a file, `$?` read immediately after).

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
