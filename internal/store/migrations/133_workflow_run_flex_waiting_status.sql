-- +goose Up
-- +goose NO TRANSACTION
-- 133_workflow_run_flex_waiting_status.sql
-- TASKS/teams/06-stepkindflex-executor.md.
--
-- Widens workflow_runs.status and workflow_run_steps.status's CHECK
-- constraints to accept a new 'waiting_on_flex' value, distinct from the
-- existing 'waiting_on_gate' value both tables already carry.
--
-- Why this is needed, and why it wasn't anticipated by 130/131: this
-- task's own "What to do" instructs "Do not silently reuse the literal
-- waiting_on_gate status for a flex step without deciding this on
-- purpose -- a flex step is not a gate" and recommends a distinct status
-- (e.g. "waiting_on_flex"). Reusing 'waiting_on_gate' for a flex-step
-- pause would make internal/service/a2a_task_manager.go's
-- deriveFromWorkflowRun (which maps run.Status == "waiting_on_gate"
-- directly to a2a.TaskStateInputRequired -- "needs human input") report
-- a flex-waiting TeamRun the same way it reports a genuinely
-- human-gated one, when a flex step is actually agents self-organizing,
-- not blocked on a human -- exactly the misbehavior this task's own text
-- warns about. A distinct, DB-persisted status string is the only way to
-- make that distinction durable across a Resume() call / process
-- restart, not just an in-memory Go-level convention -- so this
-- migration is load-bearing for task 06's "confirm every real caller of
-- Resume/anything inspecting RunStatusWaiting handles whichever status
-- you choose correctly" requirement, not optional polish.
--
-- SQLite cannot ALTER a CHECK constraint in place, so both tables are
-- rebuilt via the same rename-recreate-copy pattern
-- 130_workflow_run_steps_flex_kind.sql (workflow_run_steps.kind) and
-- 119_agent_reflex_dispatch_to_agent.sql (agent_reflexes.action_kind)
-- already used. workflow_runs has real incoming FK references
-- (089_a2a_tasks.sql's workflow_run_id, 129_team_run_members.sql's
-- workflow_run_id, 131_agent_reflexes_workflow_run_scoping.sql's
-- workflow_run_id) -- PRAGMA foreign_keys = OFF for the whole rebuild
-- (both tables) means none of those are enforced/cascaded mid-migration,
-- and by commit time both tables exist again with identical row ids, so
-- referential integrity is intact (same reasoning 119's own doc comment
-- gives for agent_reflex_opt_outs' FK to agent_reflexes).
--
-- Column shape for both tables is copied verbatim from their current
-- live shape (workflow_runs: 051_agent_workflows.sql's original CREATE
-- TABLE, unchanged since; workflow_run_steps: 130's rebuilt shape,
-- including the gate_input column 091_workflow_gate_input.sql added) --
-- only each table's status CHECK value list changes.

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
    updated_at TEXT NOT NULL DEFAULT ''
);

INSERT INTO workflow_runs_new
    (id, definition_name, status, input_json, error, started_at, completed_at, updated_at)
SELECT
    id, definition_name, status, input_json, error, started_at, completed_at, updated_at
FROM workflow_runs;

DROP TABLE workflow_runs;

ALTER TABLE workflow_runs_new RENAME TO workflow_runs;

CREATE INDEX IF NOT EXISTS idx_workflow_runs_status
    ON workflow_runs(status, started_at);

CREATE TABLE IF NOT EXISTS workflow_run_steps_new (
    id TEXT PRIMARY KEY,
    workflow_run_id TEXT NOT NULL,
    step_id TEXT NOT NULL,
    kind TEXT NOT NULL
        CHECK(kind IN ('llm','tool','gate','flex')),
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

-- +goose Down
-- Rebuilds both tables with the pre-flex-waiting status CHECKs.
-- Structure-only, matching 130/119's own precedent -- any
-- waiting_on_flex rows inserted after the Up migration would violate
-- this narrower constraint on re-insert; a genuine downgrade scenario is
-- expected to have none (StepKindFlex real execution is new
-- functionality with no live callers before this task).

PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE IF NOT EXISTS workflow_runs_new (
    id TEXT PRIMARY KEY,
    definition_name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'running'
        CHECK(status IN ('running','completed','failed','cancelled','waiting_on_gate')),
    input_json TEXT NOT NULL DEFAULT '{}',
    error TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL,
    completed_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT ''
);

INSERT INTO workflow_runs_new
    (id, definition_name, status, input_json, error, started_at, completed_at, updated_at)
SELECT
    id, definition_name, status, input_json, error, started_at, completed_at, updated_at
FROM workflow_runs;

DROP TABLE workflow_runs;

ALTER TABLE workflow_runs_new RENAME TO workflow_runs;

CREATE INDEX IF NOT EXISTS idx_workflow_runs_status
    ON workflow_runs(status, started_at);

CREATE TABLE IF NOT EXISTS workflow_run_steps_new (
    id TEXT PRIMARY KEY,
    workflow_run_id TEXT NOT NULL,
    step_id TEXT NOT NULL,
    kind TEXT NOT NULL
        CHECK(kind IN ('llm','tool','gate','flex')),
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK(status IN ('pending','running','completed','failed','waiting_on_gate','skipped')),
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
