-- +goose Up
-- +goose NO TRANSACTION
-- 130_workflow_run_steps_flex_kind.sql
-- TASKS/teams/03-stepkindflex-schema.md: widens workflow_run_steps.kind's
-- CHECK constraint to accept the new StepKindFlex ('flex') value
-- (internal/agentworkflow/types.go) needed by
-- docs/engineering/architecture/15-teams.md's Decision 2 ("TeamRun IS a
-- WorkflowRun"): a Team's phase sequence compiles to an ordinary
-- WorkflowDefinition whose fluid-coordination stretches are 'flex' steps,
-- not a new run engine. Without this migration, any code inserting a
-- workflow_run_steps row with kind='flex' (task 06's job) fails at the DB
-- layer regardless of how correct the Go-level engine logic is -- the
-- constraint 051_agent_workflows.sql originally shipped only allows
-- ('llm','tool','gate').
--
-- SQLite cannot ALTER a CHECK constraint in place, so the table is rebuilt
-- via the same rename-recreate-copy pattern used by
-- 119_agent_reflex_dispatch_to_agent.sql (itself citing
-- 074_agent_profiles_multi_agent.sql / 105_drop_unused_session_status_values.sql
-- as earlier precedent). The column shape below is the live schema as of
-- this migration: 051_agent_workflows.sql's original CREATE TABLE plus the
-- gate_input column 091_workflow_gate_input.sql appended via ALTER TABLE
-- ADD COLUMN (confirmed directly against a real backup database copy, not
-- reconstructed from the migration ledger alone -- ALTER TABLE ADD COLUMN
-- appends physically at the end of the row regardless of where a later
-- convenience column-list constant orders it). No other migration touches
-- workflow_run_steps' shape (grepped the full migrations/ directory).
--
-- Every existing column, its type/default, the status/kind CHECKs (status
-- unchanged; kind widened), and both indexes (the UNIQUE
-- (workflow_run_id, step_id) index workflow_run_steps.go's upsert/list
-- queries depend on, plus the status lookup index) are preserved exactly.
-- No FK anywhere in the migrations ledger references workflow_run_steps,
-- so PRAGMA foreign_keys = OFF during the rebuild has no cross-table
-- referential-integrity exposure here (unlike 119's agent_reflex_opt_outs
-- case, which had to reason about a real incoming FK).
--
-- launch_source_type note (durable_agent_instances, migrations 083/084):
-- this migration deliberately does NOT touch that CHECK. Whether a
-- TeamRun-launched durable slot member needs a distinct 'team_run'
-- launch_source_type value, or correctly reuses an existing generic value
-- (process_tick/task_template_run), is task 08's call once it builds the
-- real TeamRun launch path (TASKS/teams/08-team-run-launcher.md) --
-- confirmed no other Teams-batch task file currently claims this decision
-- either, so it is punted forward rather than guessed at here. See this
-- task file's own Work Log for the full reasoning.

PRAGMA foreign_keys = OFF;

BEGIN;

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

-- +goose Down
-- Rebuilds workflow_run_steps with the pre-flex 3-value kind CHECK.
-- Structure-only, matching 119's precedent -- any kind='flex' rows
-- inserted after the Up migration would violate this narrower constraint
-- on re-insert; a genuine downgrade scenario is expected to have none
-- (StepKindFlex is new functionality with no live callers yet -- task 06
-- is what would actually start inserting these rows -- not a
-- widen-then-narrow round trip over pre-existing data).

PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE IF NOT EXISTS workflow_run_steps_new (
    id TEXT PRIMARY KEY,
    workflow_run_id TEXT NOT NULL,
    step_id TEXT NOT NULL,
    kind TEXT NOT NULL
        CHECK(kind IN ('llm','tool','gate')),
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
