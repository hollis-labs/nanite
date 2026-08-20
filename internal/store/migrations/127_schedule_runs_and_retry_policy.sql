-- +goose Up
-- +goose NO TRANSACTION
-- TASKS/scheduling/01-schema-schedule-kind-collapse-and-retry-columns.md.
-- docs/engineering/architecture/12-scheduling.md ("Full replace, not
-- dual-run" and "Retry, backoff, and on_fail policy") is the design this
-- migration implements: adopting github.com/hollis-labs/go-scheduler as
-- the single replacement for the "durable-agent-wake-tick" 2-minute poller
-- and wakeScheduleDue's 15-minute-lookback heuristic (both retired by a
-- later task in this same batch, not this one).
--
-- Migration-number note: this task file's own Context section provisionally
-- named 126, against a baseline where 125_reflex_action_kind_provenance_
-- allow.sql was believed to be the latest migration. By the time this task
-- was actually implemented, a sibling batch (TASKS/harness-reactive-self-
-- tools) had already landed 126_selftool_reactions.sql on main -- confirmed
-- directly via `ls internal/store/migrations/ | sort -t_ -k1 -n | tail`
-- before writing this file, per that same Context section's own explicit
-- warning about the numbering collision. This file is 127, not 126.
--
-- Two changes:
--
--   1. agent_schedules is rebuilt (SQLite cannot ALTER a CHECK constraint
--      in place -- same rename-recreate-copy pattern as
--      119_agent_reflex_dispatch_to_agent.sql / 115_agent_reflex_opt_out.sql):
--        - schedule_kind's CHECK narrows from five values
--          ('every_n_ticks','on_tick','cron','one_shot','on_event') to two
--          ('cron','one_shot'). Confirmed directly against the real backup
--          DB (~/.local/share/nanite/workspaces/default/backups/) before
--          writing this migration: exactly one production row exists
--          (a cron-kind, active, YAML-sync-authored row), so the CHECK
--          narrowing drops no live data. The three retired values are
--          removed from the schema entirely, not kept as unimplemented
--          slots -- see the architecture doc's "Full replace, not
--          dual-run" section for why: none of the three ever had a real
--          firing mechanism (the FU-27 composer that was meant to
--          interpret them was never built, and go-scheduler's own model
--          is time-based only), and keeping the slots "for later" would
--          recreate the exact live-looking-but-isn't-really-live ambiguity
--          CW-20260819-0006 was filed to resolve.
--        - max_retries / on_fail: per-schedule retry policy the Runner
--          adapter (TASKS/scheduling/04-retry-backoff-on-fail-policy.md)
--          reads. on_fail='retry' is the illustrative default; "retry"
--          isn't itself a meaningful *terminal* action once max_retries is
--          exhausted (that's what the counted retries already did) -- see
--          this task's own Work Log for how 04 is expected to resolve
--          on_fail='retry' at exhaustion (falls through to the same
--          non-disabling behavior as 'notify').
--        - next_run: the persisted next-fire time go-scheduler's CAS claim
--          (Store.ClaimAndUpdateScheduleRun) compares against. NULL means
--          unscheduled, matching go-scheduler's own "zero NextRun is
--          skipped" convention (libs/go-scheduler/scheduler.go:28).
--          Deliberately left NULL for every row by this SQL migration
--          rather than computed here -- see internal/store/
--          agent_schedules.go's backfillScheduleNextRun doc comment for why
--          the real computation (which needs actual robfig/cron parsing,
--          not a hand-rolled SQL approximation of cron semantics) runs as
--          a Go-level, idempotent (NULL-guarded) step immediately after
--          every migrate() call in Store.New(), not as part of this SQL
--          file. It is still "backfilled as part of applying this
--          migration" end to end (same commit, runs automatically and
--          unconditionally on the very next process boot after this file
--          lands) -- just not inside the SQL statements themselves.
--        - job_type / job_payload: which of the four job types
--          (docs/engineering/architecture/12-scheduling.md's "The Runner
--          adapter and job taxonomy" table) a row represents, plus a
--          generic structured payload for the three new types. Every
--          existing row backfills to job_type='durable_agent_wake' (the
--          only value in live use today) with job_payload left at its
--          '{}' default -- 03-runner-adapter-and-job-taxonomy.md's adapter
--          reads `body` directly for that job type, so an empty
--          job_payload is correct, not a gap. `body` itself is untouched:
--          still the live durable_agent_wake payload field.
--
--   2. schedule_runs is new: one row per schedule firing (go-scheduler's
--      Job.RunID), tracking retry state across that firing's attempts for
--      04-retry-backoff-on-fail-policy.md's Runner decorator. status
--      vocabulary is pending/succeeded/failed/exhausted, taken verbatim
--      from the architecture doc's own illustrative shape -- no deviation.

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
    created_by,
    3, 'retry', NULL, 'durable_agent_wake', '{}'
FROM agent_schedules;

DROP TABLE agent_schedules;

ALTER TABLE agent_schedules_new RENAME TO agent_schedules;

CREATE INDEX IF NOT EXISTS idx_agent_schedules_lookup
    ON agent_schedules(agent_id, session_id, status);

CREATE TABLE IF NOT EXISTS schedule_runs (
    id              TEXT PRIMARY KEY,
    schedule_id     TEXT NOT NULL REFERENCES agent_schedules(id),
    run_id          TEXT NOT NULL,
    fired_at        TEXT NOT NULL,
    status          TEXT NOT NULL CHECK (
        status IN ('pending','succeeded','failed','exhausted')
    ),
    attempt_count   INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT,
    next_attempt_at TEXT
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_schedule_runs_run_id
    ON schedule_runs(run_id);

CREATE INDEX IF NOT EXISTS idx_schedule_runs_schedule_id
    ON schedule_runs(schedule_id, fired_at DESC);

END;

PRAGMA foreign_keys = ON;

-- +goose Down
-- Rebuilds agent_schedules with the pre-127 five-value CHECK and drops the
-- retry-policy/job-taxonomy columns, and drops schedule_runs entirely.
-- Structure-only, matching 119's own Down precedent: max_retries/on_fail/
-- next_run/job_type/job_payload values and any schedule_runs rows written
-- after the Up migration are discarded on downgrade, not preserved -- a
-- genuine downgrade scenario is expected to have none of consequence (this
-- is new functionality, not a widen-then-narrow round trip over pre-
-- existing data).

PRAGMA foreign_keys = OFF;

BEGIN;

DROP TABLE IF EXISTS schedule_runs;

CREATE TABLE IF NOT EXISTS agent_schedules_new (
    id              TEXT PRIMARY KEY,
    agent_id        TEXT NOT NULL REFERENCES agent_profiles(id),
    session_id      TEXT,
    name            TEXT NOT NULL,
    schedule_kind   TEXT NOT NULL CHECK (
        schedule_kind IN ('every_n_ticks','on_tick','cron','one_shot','on_event')
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
    created_by      TEXT NOT NULL DEFAULT 'operator'
);

INSERT INTO agent_schedules_new (
    id, agent_id, session_id, name, schedule_kind, schedule_spec, body,
    priority, status, expires_at, fired_count, last_fired_at, created_at,
    created_by
)
SELECT
    id, agent_id, session_id, name, schedule_kind, schedule_spec, body,
    priority, status, expires_at, fired_count, last_fired_at, created_at,
    created_by
FROM agent_schedules;

DROP TABLE agent_schedules;

ALTER TABLE agent_schedules_new RENAME TO agent_schedules;

CREATE INDEX IF NOT EXISTS idx_agent_schedules_lookup
    ON agent_schedules(agent_id, session_id, status);

END;

PRAGMA foreign_keys = ON;
