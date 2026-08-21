# `workflow_runs` loop-scoping columns — `loop_run_id`, `loop_iteration`

**Phase:** 1 — Schema & storage foundation (`TASKS/loops`)
**Status:** not-started
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
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
