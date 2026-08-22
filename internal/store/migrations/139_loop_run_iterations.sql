-- TASKS/loops/04-loop-run-iterations-schema.md.
--
-- Adds loop_run_iterations -- per-iteration history for a loop_runs row,
-- docs/engineering/architecture/21-loops.md's own illustrative example
-- ("New, thin (loop_run_iterations) -- same shape as Scheduling's
-- illustrative schedule_runs: one row per firing"). Each row records one
-- iteration's actual WorkflowRun and the continuation-policy decision made
-- against it. evaluation_json stores the per-iteration Verify rollup
-- (remaining_delta, confidence, regressions per the design doc) -- NOT a
-- duplicate of workflow_run_steps.verify_json itself; workflow_run_id is the
-- pointer back to the real per-step verify records.
--
-- Migration-number note: per this batch's dispatch instructions, this task
-- uses migration number 139 explicitly assigned by the orchestrator (NOT
-- the task file's own provisional "141" placeholder, and NOT a number
-- re-derived by this worker) -- a sibling worker in a separate isolated
-- worktree off the same base is concurrently assigned 140 for task 05, to
-- avoid the numbering collisions two prior waves in this batch hit when
-- parallel workers each independently "confirmed" the same next-available
-- number in their own isolated worktree. Confirmed via
-- `ls internal/store/migrations/139_*` returning no match in this worktree
-- immediately before writing this file -- the latest migration actually on
-- disk here is 138_loop_runs.sql (task 03).
--
-- decision/progress_state/workflow_run_id are all nullable -- per this
-- task's own "What to do" §1: task 08 creates a row when an iteration
-- *starts* (workflow_run_id may not exist yet at that instant if the
-- WorkflowRun is launched in a transaction-adjacent step) and fills in
-- decision/progress_state/completed_at once that iteration's evaluation
-- finishes. This matches 21-loops.md's own illustrative example, whose
-- iteration 3 shows `decision: null # in flight`. This migration does not
-- deviate from that sequencing -- see internal/store/loop_run_iterations.go
-- for the Go-side Create/Complete split that implements it.
--
-- idx_loop_run_iterations_seq is a UNIQUE index, not a plain one -- it is
-- both the ordering index ListLoopRunIterations relies on and the
-- constraint enforcing "one row per (loop_run_id, iteration_number)," per
-- this task's "Done means" regression-test requirement.
--
-- workflow_run_id REFERENCES workflow_runs(id) -- FK-enforced (this
-- codebase runs with PRAGMA foreign_keys=1) but nullable, so an in-flight
-- row created before the WorkflowRun exists is not itself an FK violation;
-- the FK only fires once workflow_run_id is actually set to a non-NULL
-- value (via CompleteLoopRunIteration), at which point it must reference a
-- real workflow_runs row.
--
-- No FK from loop_run_id to loop_runs(id) is skipped -- unlike
-- goal_evidence.loop_run_id (migration 137, deliberately unenforced because
-- that table can legitimately point at a loop_run_id that lands in a
-- migration ordered later than itself), loop_run_iterations always follows
-- loop_runs (138 < 139) and every row here is created only once its parent
-- loop_runs row already exists (task 08's launcher), so the ordinary
-- REFERENCES loop_runs(id) constraint applies with no special-casing,
-- matching 132_team_authority_grants.sql's own normalized-sub-table-
-- pointing-at-a-parent-definition-row precedent this task cites.
--
-- Brand-new table, nothing to rebuild -- plain transactional CREATE TABLE,
-- matching 128_teams.sql's / 135_goals.sql's / 138_loop_runs.sql's own
-- precedent (no existing rows to preserve, so no PRAGMA foreign_keys /
-- rename-recreate-copy rebuild dance is needed).

-- +goose Up
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

CREATE UNIQUE INDEX IF NOT EXISTS idx_loop_run_iterations_seq
    ON loop_run_iterations(loop_run_id, iteration_number);
CREATE INDEX IF NOT EXISTS idx_loop_run_iterations_workflow_run
    ON loop_run_iterations(workflow_run_id);

-- +goose Down
DROP TABLE IF EXISTS loop_run_iterations;
