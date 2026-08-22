-- +goose Up
-- +goose NO TRANSACTION
-- 136_workflow_run_steps_loop_kind.sql
-- TASKS/loops/06-stepkindloop-schema.md: widens workflow_run_steps.kind's
-- CHECK constraint to accept the new StepKindLoop ('loop') value
-- (internal/agentworkflow/types.go) needed by
-- docs/engineering/architecture/21-loops.md's Decision 1 ("one new, thin
-- step kind" so a Workflow can contain a Loop): a StepKindLoop step returns
-- a waiting status when it starts, and the contained LoopRun reaching a
-- terminal state is what calls Resume on the outer workflow -- the same
-- pause/external-resolve/Resume plumbing StepKindFlex already reused from
-- StepKindGate. Without this migration, any code inserting a
-- workflow_run_steps row with kind='loop' (task 09's job) fails at the DB
-- layer regardless of how correct the Go-level engine logic is.
--
-- This is schema-only, mirroring Teams' own precedent exactly
-- (130_workflow_run_steps_flex_kind.sql landed the StepKindFlex kind-CHECK
-- widening separately from the RunStatusWaitingOnFlex status-CHECK
-- widening, which landed later in 133 alongside the real executor work).
-- The equivalent RunStatusWaitingOnLoop / workflow_run(_steps).status CHECK
-- widening is explicitly NOT this task's job -- that is task 09's
-- migration, landing alongside the real StepKindLoop pause/resume wiring
-- it's needed for.
--
-- SQLite cannot ALTER a CHECK constraint in place, so the table is rebuilt
-- via the same rename-recreate-copy pattern 130 and 133 already used
-- (itself citing 119_agent_reflex_dispatch_to_agent.sql /
-- 074_agent_profiles_multi_agent.sql / 105_drop_unused_session_status_values.sql
-- as earlier precedent). The column shape and both CHECK constraints below
-- are the live schema as of this migration -- confirmed directly against a
-- real backup database copy (~/.local/share/nanite/workspaces/default/backups),
-- not reconstructed from the migration ledger alone: 051_agent_workflows.sql's
-- original CREATE TABLE, plus gate_input (091_workflow_gate_input.sql,
-- ALTER TABLE ADD COLUMN), plus 130's kind widening to include 'flex', plus
-- 133's status widening to include 'waiting_on_flex'. No migration after
-- 133 touches this table's shape (134_agent_profiles_protocol_transport.sql
-- only touches agent_profiles; grepped the full migrations/ directory to
-- confirm). Only the kind CHECK's value list changes here; the status CHECK
-- is carried forward unchanged from 133, per this task's explicit scope
-- fence.
--
-- No FK anywhere in the migrations ledger references workflow_run_steps, so
-- PRAGMA foreign_keys = OFF during the rebuild has no cross-table
-- referential-integrity exposure here (same reasoning 130's own doc
-- comment gives).

PRAGMA foreign_keys = OFF;

BEGIN;

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

-- +goose Down
-- Rebuilds workflow_run_steps with the pre-loop 5-value kind CHECK (back to
-- 130/133's state -- 'llm','tool','gate','flex' -- not further back than
-- that, matching 130/133's own downgrade precedent of restoring the
-- immediately-prior state, not the full history). Structure-only; any
-- kind='loop' rows inserted after the Up migration would violate this
-- narrower constraint on re-insert -- a genuine downgrade scenario is
-- expected to have none (StepKindLoop is new functionality with no live
-- callers yet -- task 09 is what would actually start inserting these
-- rows -- not a widen-then-narrow round trip over pre-existing data).

PRAGMA foreign_keys = OFF;

BEGIN;

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
