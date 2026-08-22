-- +goose Up
-- TASKS/loops/05-workflow-runs-loop-scoping-columns.md.
--
-- docs/engineering/architecture/21-loops.md's explicit schema-ledger line:
-- `workflow_runs.loop_run_id NULL` / `workflow_runs.loop_iteration NULL` --
-- "run-scoping FK, same pattern as migration 131's agent_reflexes scoping."
-- Every WorkflowRun launched as one loop iteration (by LoopEngine, task 08)
-- gets tagged with which loop_runs row and which iteration number it
-- belongs to -- this is how a WorkflowRun row answers "am I part of a loop,
-- and which one/which iteration" without a join through
-- loop_run_iterations (which points the other direction: LoopRun -> its
-- WorkflowRuns). Both directions are kept because they serve different
-- queries: this column answers "given a WorkflowRun, what loop is it in"
-- (useful for A2A/UI surfacing a running workflow's loop context);
-- loop_run_iterations answers "given a LoopRun, list its iterations in
-- order."
--
-- Same lightweight shape as migration 131_agent_reflexes_workflow_run_
-- scoping.sql (a plain nullable ADD COLUMN with a REFERENCES clause and no
-- CHECK constraint, plus one index) -- no PRAGMA foreign_keys toggling or
-- rename-recreate-copy rebuild dance required, since neither new column
-- carries a CHECK constraint. loop_iteration is a plain nullable INTEGER,
-- not FK'd to anything -- it's meaningful only in combination with
-- loop_run_id (non-NULL), not independently constrained.
--
-- Referencing loop_runs(id) (migration 138_loop_runs.sql, TASKS/loops/
-- 03-loop-runs-schema.md) -- this migration therefore must run after 138,
-- matching this task file's own stated dependency.
--
-- Nothing in this task sets these columns yet -- no engine calls it. That's
-- task 08's job when it launches an iteration's WorkflowRun. See this
-- task's Work Log for the documented finding on where task 08 sets these
-- two fields (WorkflowLaunchRequest has no natural pass-through; a
-- follow-up narrow updater, UpdateWorkflowRunLoopScope, is added instead --
-- internal/store/workflow_runs.go).

ALTER TABLE workflow_runs ADD COLUMN loop_run_id TEXT REFERENCES loop_runs(id);
ALTER TABLE workflow_runs ADD COLUMN loop_iteration INTEGER;

CREATE INDEX IF NOT EXISTS idx_workflow_runs_loop_run
    ON workflow_runs(loop_run_id, loop_iteration);

-- +goose Down
-- Structure-only, same precedent as 131's own Down for its own nullable FK
-- column: no pre-migration equivalent existed to restore, and a genuine
-- downgrade is expected to have no rows depending on data this Down
-- discards (no code path in this task, or any currently-landed task, sets
-- a non-NULL loop_run_id/loop_iteration -- that's task 08's job, not yet
-- filed as of this migration).

DROP INDEX IF EXISTS idx_workflow_runs_loop_run;

ALTER TABLE workflow_runs DROP COLUMN loop_iteration;
ALTER TABLE workflow_runs DROP COLUMN loop_run_id;
