# Loop run iterations schema — `loop_run_iterations` append-only table

**Phase:** 1 — Schema & storage foundation (`TASKS/loops`)
**Status:** not-started
**Depends on:** `03-loop-runs-schema.md` (`loop_run_id` FK)
**Touches:** `internal/store/migrations/` (new migration), `internal/store/loop_runs.go` or
a new `internal/store/loop_run_iterations.go` (Go types, CRUD).

## Context

Implements `docs/engineering/architecture/21-loops.md`'s per-iteration history — quoted
directly from the design doc's own schema ledger: *"New, thin (`loop_run_iterations`) —
same shape as Scheduling's illustrative `schedule_runs`: one row per firing."* Each row
records one iteration's actual `WorkflowRun` and the continuation-policy decision made
against it — the design doc's own illustrative example:
```
loop_run_iterations:
  - iteration_number: 1
    workflow_run_id: wr_abc
    decision: continue
    progress_state: progress
  - iteration_number: 2
    workflow_run_id: wr_def
    decision: replan
    progress_state: no_progress
```
Per-iteration evaluation reuses `Verify` wholesale (design doc, "Continuation policy"
section): *"an iteration's `IterationResult.progress` is a rollup over that iteration's
`WorkflowRun` step `VerifyResult`s, not a second evaluator subsystem."* This table's
`evaluation_json` column stores that rollup (`remaining_delta`, `confidence`,
`regressions`, per the design doc), not a duplicate of `workflow_run_steps.verify_json`
itself — `workflow_run_id` is the pointer back to the real per-step verify records.

## What to do

1. **New migration** — re-verify the actual next-available migration number at dispatch
   time; this task provisionally claims `141`. Illustrative DDL, following
   `132_team_authority_grants.sql`'s "normalized sub-table" shape:
   ```sql
   CREATE TABLE IF NOT EXISTS loop_run_iterations (
       id                TEXT PRIMARY KEY,
       loop_run_id       TEXT NOT NULL REFERENCES loop_runs(id),
       iteration_number  INTEGER NOT NULL,
       workflow_run_id   TEXT REFERENCES workflow_runs(id),
       decision          TEXT CHECK (decision IN ('continue','retry','replan','rearchitect',
                                 'wait','escalate','complete','fail')),
       progress_state    TEXT CHECK (progress_state IN ('progress','no_progress','regression',
                                 'blocked','goal_met')),
       evaluation_json   TEXT NOT NULL DEFAULT '{}',
       started_at        TEXT NOT NULL DEFAULT (datetime('now')),
       completed_at      TEXT
   );
   CREATE UNIQUE INDEX idx_loop_run_iterations_seq ON loop_run_iterations(loop_run_id, iteration_number);
   CREATE INDEX idx_loop_run_iterations_workflow_run ON loop_run_iterations(workflow_run_id);
   ```
   `decision`/`progress_state` are both nullable (`workflow_run_id` too) because task `08`
   creates a row when an iteration *starts* (`workflow_run_id` may not exist yet at that
   instant if the WorkflowRun is launched in the same transaction-adjacent step) and fills
   in `decision`/`progress_state`/`completed_at` once that iteration's evaluation finishes —
   matching the design doc's own illustrative example, whose iteration 3 shows
   `decision: null # in flight`. Document your call if you sequence it differently (e.g.
   only inserting the row once `workflow_run_id` is known).

2. **Go types + store CRUD**, matching task `03`'s file/package: `LoopRunIteration` struct;
   `Evaluation` struct (`RemainingDelta string`, `Confidence float64`, `Regressions []string`
   — or your own reasonable shape matching the design doc's named fields) decoded from
   `evaluation_json`; `CreateLoopRunIteration`, `GetLoopRunIteration(loopRunID, iterationNumber)`,
   `ListLoopRunIterations(loopRunID)` (ordered by `iteration_number`),
   `CompleteLoopRunIteration(id, workflowRunID, decision, progressState, evaluation)` (the
   one mutator — fills in the fields left null at creation), `ErrLoopRunIterationNotFound`.

## Done means

- Migration applies cleanly against a real backup copy of the database.
- CRUD round-trips correctly: create an in-flight row (`decision`/`progress_state` null),
  then complete it, then verify `ListLoopRunIterations` returns it in the completed state
  ordered correctly alongside a second, still-in-flight row.
- `idx_loop_run_iterations_seq`'s uniqueness is exercised by a regression test (duplicate
  `(loop_run_id, iteration_number)` insert fails).
- This task is storage-only. `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
