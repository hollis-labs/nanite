-- +goose Up
-- +goose NO TRANSACTION
-- 143_agent_schedules_loop_run_tick_job_type.sql
-- TASKS/loops/12-loop-run-tick-scheduled-trigger.md.
--
-- Widens agent_schedules.job_type's CHECK constraint (migration 127) from
-- the four Scheduling-era values ('durable_agent_wake','agent_workflow_run',
-- 'command_run','reflex_dispatch') to five, adding 'loop_run_tick' --
-- docs/engineering/architecture/21-loops.md's "Trigger surface" section:
-- "a new loop_run_tick JobType, the fifth alongside Scheduling's
-- already-enumerated durable_agent_wake/agent_workflow_run/command_run/
-- reflex_dispatch ... for a 'durable'-preset loop or a WAIT-status loop
-- polling an external condition." internal/scheduler/runner_adapter.go
-- (JobTypeLoopRunTick) and internal/scheduler/store_adapter.go (buildPayload's
-- pass-through case) are this same task's matching application-layer
-- changes; this migration is the DB-side CHECK widening those two files'
-- own job_type strings need in order to ever be persisted at all.
--
-- Migration-number note: this task's own dispatch-time assignment is 143,
-- not the next sequential-looking number after 138-141 -- a sibling task in
-- this same parallel batch (TASKS/loops/11) is concurrently assigned 142 in
-- an isolated worktree off the same base, to avoid the exact same-wave
-- numbering collision this batch has already hit twice. Confirmed directly
-- via `ls internal/store/migrations/ | sort -t_ -k1 -n | tail` before
-- writing this file: no 142_*.sql or 143_*.sql exists in this worktree at
-- write time.
--
-- SQLite cannot ALTER a CHECK constraint in place, so agent_schedules is
-- rebuilt via the same rename-recreate-copy pattern migration 127 itself
-- used (and 130/133/136/141 reused for other tables) -- every column and
-- every other CHECK/DEFAULT is copied verbatim from 127's shape; only the
-- job_type CHECK's value list changes. schedule_runs (127) has a real FK
-- into agent_schedules(id) -- PRAGMA foreign_keys = OFF for the rebuild
-- means that FK isn't enforced/cascaded mid-migration, and by commit time
-- agent_schedules exists again with identical row ids, so referential
-- integrity is intact (same reasoning 127/133/141's own doc comments give
-- for their own rebuilds). schedule_runs itself is untouched -- this
-- migration does not recreate it.

PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE IF NOT EXISTS agent_schedules_new (
    id              TEXT PRIMARY KEY,
    agent_id        TEXT NOT NULL REFERENCES agent_profiles(id),
    session_id      TEXT,
    name            TEXT NOT NULL,
    schedule_kind   TEXT NOT NULL CHECK (
        schedule_kind IN ('cron','one_shot')
    ),
    schedule_spec   TEXT NOT NULL DEFAULT '',
    body            TEXT NOT NULL,
    priority        INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'active' CHECK (
        status IN ('active','paused','expired')
    ),
    expires_at      TEXT,
    fired_count     INTEGER NOT NULL DEFAULT 0,
    last_fired_at   TEXT,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    created_by      TEXT NOT NULL DEFAULT 'operator',
    max_retries     INTEGER NOT NULL DEFAULT 3,
    on_fail         TEXT NOT NULL DEFAULT 'retry' CHECK (
        on_fail IN ('retry','disable','notify')
    ),
    next_run        TEXT,
    job_type        TEXT NOT NULL DEFAULT 'durable_agent_wake' CHECK (
        job_type IN ('durable_agent_wake','agent_workflow_run','command_run','reflex_dispatch','loop_run_tick')
    ),
    job_payload     TEXT NOT NULL DEFAULT '{}'
);

INSERT INTO agent_schedules_new (
    id, agent_id, session_id, name, schedule_kind, schedule_spec, body,
    priority, status, expires_at, fired_count, last_fired_at, created_at,
    created_by, max_retries, on_fail, next_run, job_type, job_payload
)
SELECT
    id, agent_id, session_id, name, schedule_kind, schedule_spec, body,
    priority, status, expires_at, fired_count, last_fired_at, created_at,
    created_by, max_retries, on_fail, next_run, job_type, job_payload
FROM agent_schedules;

DROP TABLE agent_schedules;

ALTER TABLE agent_schedules_new RENAME TO agent_schedules;

CREATE INDEX IF NOT EXISTS idx_agent_schedules_lookup
    ON agent_schedules(agent_id, session_id, status);

END;

PRAGMA foreign_keys = ON;

-- +goose Down
-- Rebuilds agent_schedules with the pre-loop_run_tick four-value job_type
-- CHECK (127's shape). Structure-only, matching 127/133/136/141's own
-- downgrade precedent: any row inserted with job_type='loop_run_tick' after
-- the Up migration would violate this narrower CHECK on re-insert -- a
-- genuine downgrade scenario is expected to have none of consequence (this
-- is new functionality with no live producer before this task).

PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE IF NOT EXISTS agent_schedules_new (
    id              TEXT PRIMARY KEY,
    agent_id        TEXT NOT NULL REFERENCES agent_profiles(id),
    session_id      TEXT,
    name            TEXT NOT NULL,
    schedule_kind   TEXT NOT NULL CHECK (
        schedule_kind IN ('cron','one_shot')
    ),
    schedule_spec   TEXT NOT NULL DEFAULT '',
    body            TEXT NOT NULL,
    priority        INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'active' CHECK (
        status IN ('active','paused','expired')
    ),
    expires_at      TEXT,
    fired_count     INTEGER NOT NULL DEFAULT 0,
    last_fired_at   TEXT,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    created_by      TEXT NOT NULL DEFAULT 'operator',
    max_retries     INTEGER NOT NULL DEFAULT 3,
    on_fail         TEXT NOT NULL DEFAULT 'retry' CHECK (
        on_fail IN ('retry','disable','notify')
    ),
    next_run        TEXT,
    job_type        TEXT NOT NULL DEFAULT 'durable_agent_wake' CHECK (
        job_type IN ('durable_agent_wake','agent_workflow_run','command_run','reflex_dispatch')
    ),
    job_payload     TEXT NOT NULL DEFAULT '{}'
);

INSERT INTO agent_schedules_new (
    id, agent_id, session_id, name, schedule_kind, schedule_spec, body,
    priority, status, expires_at, fired_count, last_fired_at, created_at,
    created_by, max_retries, on_fail, next_run, job_type, job_payload
)
SELECT
    id, agent_id, session_id, name, schedule_kind, schedule_spec, body,
    priority, status, expires_at, fired_count, last_fired_at, created_at,
    created_by, max_retries, on_fail, next_run, job_type, job_payload
FROM agent_schedules;

DROP TABLE agent_schedules;

ALTER TABLE agent_schedules_new RENAME TO agent_schedules;

CREATE INDEX IF NOT EXISTS idx_agent_schedules_lookup
    ON agent_schedules(agent_id, session_id, status);

END;

PRAGMA foreign_keys = ON;
