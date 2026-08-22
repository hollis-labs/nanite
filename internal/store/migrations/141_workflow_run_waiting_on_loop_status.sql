-- +goose Up
-- +goose NO TRANSACTION
-- 141_workflow_run_waiting_on_loop_status.sql
-- TASKS/loops/09-stepkindloop-executor-and-waiting-status.md.
--
-- Widens workflow_runs.status and workflow_run_steps.status's CHECK
-- constraints to accept a new 'waiting_on_loop' value, distinct from the
-- existing 'waiting_on_gate'/'waiting_on_flex' values both tables already
-- carry (migration 133) -- exactly the same "a new step kind's pause is not
-- the same as a gate's pause" reasoning 133 itself documents, applied a
-- third time: docs/engineering/architecture/21-loops.md Decision 1's own
-- framing, quoted directly in this task's Context, is that a StepKindLoop
-- step "reuses the identical pause/external-resolve/Resume plumbing
-- StepKindFlex already reused from StepKindGate," and a workflow paused on
-- a contained loop needs its own distinct status so
-- internal/service/a2a_task_manager.go's deriveFromWorkflowRun (and any
-- other consumer inspecting workflow_runs.status) can tell all three pause
-- reasons apart -- reusing 'waiting_on_gate' or 'waiting_on_flex' for a
-- loop-step pause would misreport it as needing human input, or as an
-- agents-self-organizing flex phase, when it's actually neither.
--
-- Also adds workflow_run_steps.loop_run_id (nullable, REFERENCES
-- loop_runs(id), same shape as migration 140's workflow_runs.loop_run_id)
-- -- this task's own documented "your call" resolution for "where does the
-- loop_run_id get recorded on the step": a real column, not a JSON field,
-- since the step needs it looked up in both directions (given the step,
-- find its loop_run_id; given a terminal loop_run_id, find the step
-- waiting on it -- GetWorkflowRunStepByLoopRunID, workflow_runs.go) and a
-- real column supports an index for the second direction where a JSON
-- field inside e.g. output/verify_json would not. Bundled into this same
-- migration rather than a separate one purely because both tables are
-- already being rebuilt here for the status CHECK widening (SQLite cannot
-- ALTER a CHECK constraint in place) -- adding one more column to the
-- workflow_run_steps rebuild is free at that point, not a second
-- rename-recreate-copy pass. workflow_runs does NOT get an equivalent new
-- column here: it already has loop_run_id/loop_iteration from migration
-- 140 (added when a WorkflowRun IS one loop iteration; this task's new
-- column is the opposite direction -- when a WorkflowRun's own STEP
-- launched a loop) and both are carried through this rebuild unchanged.
--
-- SQLite cannot ALTER a CHECK constraint in place, so both tables are
-- rebuilt via the same rename-recreate-copy pattern migrations
-- 130/133/136 already used. workflow_runs has real incoming FK references
-- (089_a2a_tasks.sql, 129_team_run_members.sql, 131_agent_reflexes_
-- workflow_run_scoping.sql) -- PRAGMA foreign_keys = OFF for the whole
-- rebuild (both tables) means none of those are enforced/cascaded
-- mid-migration, and by commit time both tables exist again with identical
-- row ids, so referential integrity is intact (same reasoning 133's own
-- doc comment gives).
--
-- Column shape for both tables is copied verbatim from their current live
-- shape: workflow_runs per migration 140 (133's rebuilt shape plus 140's
-- loop_run_id/loop_iteration ADD COLUMNs); workflow_run_steps per migration
-- 136 (133's rebuilt shape plus 136's kind widening), plus this migration's
-- own new loop_run_id column. Confirmed directly against the migrations
-- ledger (no migration after 136/140 touches either table's shape before
-- this one) rather than reconstructed from memory alone.

PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE IF NOT EXISTS workflow_runs_new (
    id TEXT PRIMARY KEY,
    definition_name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'running'
        CHECK(status IN ('running','completed','failed','cancelled','waiting_on_gate','waiting_on_flex','waiting_on_loop')),
    input_json TEXT NOT NULL DEFAULT '{}',
    error TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL,
    completed_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT '',
    loop_run_id TEXT REFERENCES loop_runs(id),
    loop_iteration INTEGER
);

INSERT INTO workflow_runs_new
    (id, definition_name, status, input_json, error, started_at, completed_at, updated_at, loop_run_id, loop_iteration)
SELECT
    id, definition_name, status, input_json, error, started_at, completed_at, updated_at, loop_run_id, loop_iteration
FROM workflow_runs;

DROP TABLE workflow_runs;

ALTER TABLE workflow_runs_new RENAME TO workflow_runs;

CREATE INDEX IF NOT EXISTS idx_workflow_runs_status
    ON workflow_runs(status, started_at);

CREATE INDEX IF NOT EXISTS idx_workflow_runs_loop_run
    ON workflow_runs(loop_run_id, loop_iteration);

CREATE TABLE IF NOT EXISTS workflow_run_steps_new (
    id TEXT PRIMARY KEY,
    workflow_run_id TEXT NOT NULL,
    step_id TEXT NOT NULL,
    kind TEXT NOT NULL
        CHECK(kind IN ('llm','tool','gate','flex','loop')),
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK(status IN ('pending','running','completed','failed','waiting_on_gate','waiting_on_flex','waiting_on_loop','skipped')),
    output TEXT NOT NULL DEFAULT '',
    is_error INTEGER NOT NULL DEFAULT 0,
    tool_calls_json TEXT NOT NULL DEFAULT '[]',
    verify_json TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL DEFAULT '',
    completed_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT '',
    gate_input TEXT NOT NULL DEFAULT '',
    loop_run_id TEXT REFERENCES loop_runs(id)
);

INSERT INTO workflow_run_steps_new
    (id, workflow_run_id, step_id, kind, status, output, is_error,
     tool_calls_json, verify_json, error, started_at, completed_at,
     updated_at, gate_input, loop_run_id)
SELECT
    id, workflow_run_id, step_id, kind, status, output, is_error,
    tool_calls_json, verify_json, error, started_at, completed_at,
    updated_at, gate_input, NULL
FROM workflow_run_steps;

DROP TABLE workflow_run_steps;

ALTER TABLE workflow_run_steps_new RENAME TO workflow_run_steps;

CREATE UNIQUE INDEX IF NOT EXISTS idx_workflow_run_steps_run_step
    ON workflow_run_steps(workflow_run_id, step_id);

CREATE INDEX IF NOT EXISTS idx_workflow_run_steps_status
    ON workflow_run_steps(workflow_run_id, status);

END;

PRAGMA foreign_keys = ON;

-- +goose Down
-- Rebuilds both tables with the pre-loop-waiting-status CHECKs (133/136's
-- shape) and drops workflow_run_steps.loop_run_id. Structure-only, matching
-- 130/133/136's own downgrade precedent: any waiting_on_loop row, or any
-- row with a non-NULL loop_run_id, inserted after the Up migration would
-- either violate this narrower CHECK or simply lose that column's value on
-- re-insert; a genuine downgrade scenario is expected to have none
-- (StepKindLoop real execution is new functionality with no live callers
-- before this task).

PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE IF NOT EXISTS workflow_runs_new (
    id TEXT PRIMARY KEY,
    definition_name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'running'
        CHECK(status IN ('running','completed','failed','cancelled','waiting_on_gate','waiting_on_flex')),
    input_json TEXT NOT NULL DEFAULT '{}',
    error TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL,
    completed_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT '',
    loop_run_id TEXT REFERENCES loop_runs(id),
    loop_iteration INTEGER
);

INSERT INTO workflow_runs_new
    (id, definition_name, status, input_json, error, started_at, completed_at, updated_at, loop_run_id, loop_iteration)
SELECT
    id, definition_name, status, input_json, error, started_at, completed_at, updated_at, loop_run_id, loop_iteration
FROM workflow_runs;

DROP TABLE workflow_runs;

ALTER TABLE workflow_runs_new RENAME TO workflow_runs;

CREATE INDEX IF NOT EXISTS idx_workflow_runs_status
    ON workflow_runs(status, started_at);

CREATE INDEX IF NOT EXISTS idx_workflow_runs_loop_run
    ON workflow_runs(loop_run_id, loop_iteration);

CREATE TABLE IF NOT EXISTS workflow_run_steps_new (
    id TEXT PRIMARY KEY,
    workflow_run_id TEXT NOT NULL,
    step_id TEXT NOT NULL,
    kind TEXT NOT NULL
        CHECK(kind IN ('llm','tool','gate','flex','loop')),
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK(status IN ('pending','running','completed','failed','waiting_on_gate','waiting_on_flex','skipped')),
    output TEXT NOT NULL DEFAULT '',
    is_error INTEGER NOT NULL DEFAULT 0,
    tool_calls_json TEXT NOT NULL DEFAULT '[]',
    verify_json TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL DEFAULT '',
    completed_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT '',
    gate_input TEXT NOT NULL DEFAULT ''
);

INSERT INTO workflow_run_steps_new
    (id, workflow_run_id, step_id, kind, status, output, is_error,
     tool_calls_json, verify_json, error, started_at, completed_at,
     updated_at, gate_input)
SELECT
    id, workflow_run_id, step_id, kind, status, output, is_error,
    tool_calls_json, verify_json, error, started_at, completed_at,
    updated_at, gate_input
FROM workflow_run_steps;

DROP TABLE workflow_run_steps;

ALTER TABLE workflow_run_steps_new RENAME TO workflow_run_steps;

CREATE UNIQUE INDEX IF NOT EXISTS idx_workflow_run_steps_run_step
    ON workflow_run_steps(workflow_run_id, step_id);

CREATE INDEX IF NOT EXISTS idx_workflow_run_steps_status
    ON workflow_run_steps(workflow_run_id, status);

END;

PRAGMA foreign_keys = ON;
