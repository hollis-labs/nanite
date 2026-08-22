# Loop run iterations schema — `loop_run_iterations` append-only table

**Phase:** 1 — Schema & storage foundation (`TASKS/loops`)
**Status:** reviewed
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

**Migration number:** used `139` exactly, per this batch's dispatch instructions (not the
task file's own provisional "141" placeholder, and not a number re-derived by re-verifying
"next available" locally). Confirmed via `ls internal/store/migrations/139_*` returning no
match in this worktree before writing the file — the latest migration actually on disk here
was `138_loop_runs.sql` (task 03, already landed). No anomaly found.

**Files added:**
- `internal/store/migrations/139_loop_run_iterations.sql` — `loop_run_iterations` table,
  matching the task's illustrative DDL almost verbatim (decision/progress_state CHECK enums,
  nullable `workflow_run_id`/`decision`/`progress_state`, unique `(loop_run_id,
  iteration_number)` index, non-unique index on `workflow_run_id`). One addition beyond the
  illustrative DDL: `IF NOT EXISTS` on both indexes (matching 138's own precedent) for
  idempotent re-run safety.
- `internal/store/loop_run_iterations.go` — `LoopRunIteration` struct, `Evaluation` struct
  (`RemainingDelta string`, `Confidence float64`, `Regressions []string` — matches the task's
  suggested shape exactly) with `Evaluation()`/`SetEvaluation()` typed accessors mirroring
  `loop_runs.go`'s `Budget()`/`SetBudget()` convention; `LoopRunIterationDecision*` and
  `LoopRunIterationProgress*` enum constants + Go-layer validators (mirroring
  `validateLoopRunStatus`'s pattern) for the two CHECK-constrained columns;
  `ErrLoopRunIterationNotFound`; `CreateLoopRunIteration`, `GetLoopRunIteration(ctx,
  loopRunID, iterationNumber)`, `ListLoopRunIterations(ctx, loopRunID)` (ordered by
  `iteration_number` ascending), `CompleteLoopRunIteration(ctx, id, workflowRunID, decision,
  progressState, evaluation)` — the one mutator, matching the task's named signature exactly
  (ctx prepended, consistent with every other function in this package).
- `internal/store/loop_run_iterations_test.go` — round-trip (in-flight create →
  `CompleteLoopRunIteration` → verify), `ListLoopRunIterations` ordering with one completed
  row and one still-in-flight row coexisting (the design doc's own iteration-3 "in flight"
  shape), the required unique-index regression test (duplicate `(loop_run_id,
  iteration_number)` insert fails; a different `loop_run_id` may reuse the same
  `iteration_number`), decision/progress_state enum validation at both `Create` and
  `Complete`, not-found handling, and FK enforcement for both `loop_run_id` and
  `workflow_run_id`. Added a `makeTestLoopRun` test helper (mirrors the existing
  `makeTestGoal`/`makeTestWorkflowRun` helpers already in this package) since no test file
  in this batch had yet built a full goal → loop_run chain for iteration tests to hang off.

**Design calls made (not specified by the task, both narrow and consistent with the
sibling files it named as precedent):**
- `CreateLoopRunIteration` validates `Decision`/`ProgressState` only if non-empty, since the
  task's own "What to do" §1 explicitly names the in-flight (null `decision`/`progress_state`)
  shape as the normal creation path (task 08's usage). A caller that already knows both
  values up front may still pass them at creation time — validated the same way either path.
- `CompleteLoopRunIteration` requires a non-empty `workflowRunID` and validates `decision`/
  `progressState` as real enum members before writing — "the one mutator" completing an
  iteration should never write a not-yet-legal decision or a still-unknown WorkflowRun
  pointer, matching the "typed Go error before a raw CHECK/FK violation" discipline every
  other file in this package already uses.
- No FK from `loop_run_iterations.loop_run_id` to `loop_runs(id)` needed the
  `goal_evidence.loop_run_id`-style "no FK, ordering-independent" workaround: unlike
  `goal_evidence` (deliberately unenforced per migration 137's own doc comment, since that
  table's migration lands before `loop_runs`' own), `loop_run_iterations` (139) is ordered
  strictly after `loop_runs` (138) and every row is created only once its parent already
  exists — so this table uses an ordinary enforced `REFERENCES loop_runs(id)`, matching
  `132_team_authority_grants.sql`'s cited "normalized sub-table pointing at a parent
  definition row" precedent directly, with no special-casing needed.

**Verification (real exit codes and output text read directly, not through a masking
pipe):**
- `go build ./cmd/nanite/` — passes, no output.
- `go vet ./internal/store/...` (this task's actual touched package) — passes clean, no
  findings.
- `go vet ./...` (whole repo) — fails, but **only** on a pre-existing, unrelated finding in
  `internal/service/container.go` (`stopReaper`/`stopRuntimeReaper` "not used on all paths"
  possible context leak) that this task never touches — confirmed via `git status` (only
  this task's 3 new files are present in this worktree) and `git log --oneline -- 
  internal/service/container.go` (last touched by `TASKS/phase-6/04`, an unrelated batch).
  Noting this rather than silently claiming "go vet ./... passes" outright, per this task's
  own instruction to verify real exit codes/output rather than a masked signal — this
  worktree's Bash tool routes `go` invocations through an internal output-filtering wrapper
  ("tokf") whose own reported shell exit code was observed to sometimes read back `0` even
  when the wrapped command's real, printed result was a failure; the actual failure text
  (or its absence) was read directly from captured output on every check reported here, not
  inferred from `$?` alone.
- `go test ./...` (whole repo) — passes; every package reports `ok`, including
  `internal/store` (which contains this task's 7 new tests, `TestLoopRunIteration_*`, all
  passing, confirmed individually via `-v`).
- Migration tested against a real backup DB copy: copied
  `~/.local/share/nanite/workspaces/default/backups/main.db.pre-execution-backup-20260818-132726`
  (plus its `-shm`/`-wal` siblings) into scratch space and opened it via `store.New`. This
  backup predates the goose cutover entirely (no `goose_db_version` table, only legacy
  pre-migration-128 application tables) — a real exercise of the legacy-ledger-seeding path
  (`isPreGooseDatabase`/`seedLegacyLedger`) followed by every real migration from 95 through
  139 applying for real, not just this one. Confirmed `goose_db_version` reached 139, the
  `loop_run_iterations` table exists, and a full `Goal` → `LoopRun` → `LoopRunIteration`
  create/get round-trip succeeded against the migrated copy. The disposable verification
  test file used for this was deleted afterward — not part of this task's committed
  deliverable.

## Review notes

**Pass.** Fresh review (independent, no shared context with the implementing worker), Wave 3
of the Loops batch, closing out Phase 1.

Checked:
- Migration `139_loop_run_iterations.sql`: `CREATE TABLE IF NOT EXISTS loop_run_iterations`
  matches the illustrative DDL (decision/progress_state CHECK enums, nullable
  `workflow_run_id`/`decision`/`progress_state`, `evaluation_json` default `'{}'`). Confirmed
  `loop_run_id REFERENCES loop_runs(id)` is an ordinary enforced FK (not the
  `goal_evidence`-style deliberately-unenforced pointer) and that this is the correct call
  given migration ordering (139 lands strictly after 138).
- `idx_loop_run_iterations_seq` uniqueness is genuinely enforced —
  `TestLoopRunIteration_UniqueSeqConstraint` inserts a duplicate `(loop_run_id,
  iteration_number)` pair and confirms the second insert fails; a different `loop_run_id`
  reusing the same `iteration_number` succeeds, confirming the index is scoped per
  `loop_run_id` as intended.
- The in-flight → complete lifecycle matches `21-loops.md`'s own illustrative example
  (iteration 3, `decision: null # in flight`) exactly:
  `TestLoopRunIteration_RoundTrip` creates a row with `workflow_run_id`/`decision`/
  `progress_state` all empty, then `CompleteLoopRunIteration` fills them in;
  `TestLoopRunIteration_ListOrderedWithInFlightRow` exercises a completed row and a
  still-in-flight row coexisting, ordered correctly by `iteration_number`.
- FK enforcement independently re-verified for both `loop_run_id` (bogus id rejected) and
  `workflow_run_id` (bogus id rejected at `CompleteLoopRunIteration` time) — both covered by
  dedicated regression tests (`TestLoopRunIteration_LoopRunFKEnforced`/
  `_WorkflowRunFKEnforced`), both re-run directly and confirmed passing.
- `Evaluation` struct/JSON round-trip, decision/progress_state enum validation at both
  `Create`/`Complete`, and not-found handling all covered and passing.
- No naming collisions: no other `type Evaluation struct` exists in the tree; `LoopRunIteration`
  vocabulary matches `21-loops.md`/`GLOSSARY.md` (`LoopRun` entry) with no new ambiguous terms.
- Ran `go build ./cmd/nanite/` (clean), `go vet ./...` (clean except the two pre-existing,
  unrelated `internal/service/container.go` `stopReaper`/`stopRuntimeReaper` findings —
  confirmed via `git diff HEAD -- internal/service/container.go` producing zero output, so
  untouched by this task), and the full `go test ./...` (89 packages `ok`, zero `FAIL`/`panic`
  lines, real exit code 0 checked directly from a file redirect, never through a pipe to
  `tail`). Also ran this task's own `internal/store` tests targeted and verbose to confirm
  each of the 7 new `TestLoopRunIteration_*` cases individually passes.

**Consistency-gap note, not a blocking finding:** this is the second Phase 1 task in this
batch (after task `03`) to skip a permanent `migration_139_..._backup_test.go` in favor of a
throwaway, deleted verification script — see this task's own Work Log for the real-backup
exercise performed. Both `03` and `04` are brand-new `CREATE TABLE IF NOT EXISTS` migrations
with no existing rows to preserve and no rebuild dance, which is a materially lower-risk
migration shape than the rename-recreate-copy rebuilds (`130`/`133`/`136`) that motivated the
permanent-backup-test convention in the first place — so the judgment call is defensible on
its own terms each time. But now that it's happened twice in a row for the same migration
shape, it's worth the Orchestrator writing down explicitly (e.g. in
`EXECUTION-PROCESS.md` or `TASKS/ESCALATIONS.md`) that a brand-new, no-rebuild `CREATE TABLE`
migration does not require a permanent real-backup test file — a throwaway, Work-Log-documented
verification is sufficient — so future workers/reviewers don't re-litigate this from scratch
each time it comes up. Not asking for a fix to this task.

No issues found. Marking `reviewed`.
