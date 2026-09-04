-- +goose Up
-- +goose NO TRANSACTION
-- CW-20260904-0041: adapt schedule_runs to go-scheduler v0.2.0's durable
-- Fire contract. The application keeps its existing table name and row id,
-- while run_id becomes the stable Fire.ID exposed to the scheduler adapter.
--
-- Legacy status mapping is deterministic:
--   pending   -> pending
--   failed    -> retrying
--   succeeded -> succeeded
--   exhausted -> exhausted
--
-- Existing failed/pending rows represent an occurrence whose v0.1 rollback
-- left agent_schedules.next_run due. Recurring rows have next_run cleared here;
-- Store.New's post-migration backfill moves them to the next cron occurrence,
-- while the migrated Fire retains the old occurrence. One-shot rows are made
-- expired because their migrated Fire is the only occurrence they may emit.
-- The old schema did not persist scheduled time, retry policy, job type, or
-- payload per firing. scheduled_at therefore uses the earliest durable time
-- available (fired_at), while policy and job data are snapshotted from the
-- referenced agent_schedules row during this one-time migration. For
-- durable_agent_wake, the old adapter synthesized the payload from the
-- schedule and its single matching durable instance; the SELECT below performs
-- that same deterministic reconstruction.

PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE schedule_runs_new (
    id                       TEXT PRIMARY KEY,
    schedule_id              TEXT NOT NULL REFERENCES agent_schedules(id),
    run_id                   TEXT NOT NULL,
    scheduled_at             TEXT NOT NULL,
    fired_at                 TEXT NOT NULL,
    claim_expires_at         TEXT,
    dispatch_accepted_at     TEXT,
    status                   TEXT NOT NULL CHECK (
        status IN ('pending','claimed','retrying','succeeded','skipped','exhausted')
    ),
    attempt_count            INTEGER NOT NULL DEFAULT 0,
    last_error               TEXT,
    next_attempt_at          TEXT,
    retry_max_attempts       INTEGER NOT NULL DEFAULT 0,
    retry_backoff_strategy   TEXT NOT NULL DEFAULT 'none' CHECK (
        retry_backoff_strategy IN ('none','constant','linear','exponential')
    ),
    retry_initial_delay_ns   INTEGER NOT NULL DEFAULT 0,
    retry_max_delay_ns       INTEGER NOT NULL DEFAULT 0,
    job_type                 TEXT NOT NULL,
    job_payload              TEXT NOT NULL
);

INSERT INTO schedule_runs_new (
    id, schedule_id, run_id, scheduled_at, fired_at, claim_expires_at,
    dispatch_accepted_at,
    status, attempt_count, last_error, next_attempt_at,
    retry_max_attempts, retry_backoff_strategy, retry_initial_delay_ns,
    retry_max_delay_ns, job_type, job_payload
)
SELECT
    r.id,
    r.schedule_id,
    r.run_id,
    r.fired_at,
    r.fired_at,
    NULL,
    NULL,
    CASE r.status
        WHEN 'failed' THEN 'retrying'
        ELSE r.status
    END,
    r.attempt_count,
    r.last_error,
    r.next_attempt_at,
    s.max_retries,
    'exponential',
    30000000000,
    300000000000,
    s.job_type,
    CASE
        WHEN s.job_type = 'durable_agent_wake' THEN json_object(
            'instance_id', COALESCE((
                SELECT i.id
                  FROM durable_agent_instances AS i
                 WHERE i.profile_id = s.agent_id
                 ORDER BY i.id
                 LIMIT 1
            ), ''),
            'prompt', s.body
        )
        ELSE s.job_payload
    END
FROM schedule_runs AS r
JOIN agent_schedules AS s ON s.id = r.schedule_id;

-- Count every v0.1 durable run exactly once in the operator projection. The
-- retired RetryingRunner never updated fired_count, so these rows have not
-- previously contributed to the counter.
UPDATE agent_schedules
   SET fired_count = fired_count + (
       SELECT COUNT(*) FROM schedule_runs AS r WHERE r.schedule_id = agent_schedules.id
   )
 WHERE EXISTS (
       SELECT 1 FROM schedule_runs AS r WHERE r.schedule_id = agent_schedules.id
   );

-- Do not let the schedule due row rematerialize an occurrence already carried
-- by a migrated open Fire. Store.New backfills recurring NULL next_run values
-- after goose completes; a one-shot with an open Fire is already represented.
UPDATE agent_schedules
   SET next_run = CASE WHEN schedule_kind = 'cron' THEN NULL ELSE next_run END,
       status = CASE WHEN schedule_kind = 'one_shot' THEN 'expired' ELSE status END
 WHERE id IN (
       SELECT schedule_id
         FROM schedule_runs
        WHERE status IN ('pending', 'failed')
   );

DROP TABLE schedule_runs;

ALTER TABLE schedule_runs_new RENAME TO schedule_runs;

CREATE UNIQUE INDEX idx_schedule_runs_run_id
    ON schedule_runs(run_id);

CREATE INDEX idx_schedule_runs_schedule_id
    ON schedule_runs(schedule_id, scheduled_at DESC);

CREATE INDEX idx_schedule_runs_due
    ON schedule_runs(status, next_attempt_at, claim_expires_at);

END;

PRAGMA foreign_keys = ON;

-- +goose Down
-- Structural rollback to migration 127's schedule_runs shape. v0.2-only
-- statuses are mapped to the closest old vocabulary so the table rebuild is
-- total: claimed -> pending, retrying -> failed, skipped -> succeeded.

PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE schedule_runs_old (
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

INSERT INTO schedule_runs_old (
    id, schedule_id, run_id, fired_at, status, attempt_count,
    last_error, next_attempt_at
)
SELECT
    id,
    schedule_id,
    run_id,
    fired_at,
    CASE status
        WHEN 'claimed' THEN 'pending'
        WHEN 'retrying' THEN 'failed'
        WHEN 'skipped' THEN 'succeeded'
        ELSE status
    END,
    attempt_count,
    last_error,
    next_attempt_at
FROM schedule_runs;

DROP TABLE schedule_runs;

ALTER TABLE schedule_runs_old RENAME TO schedule_runs;

CREATE UNIQUE INDEX idx_schedule_runs_run_id
    ON schedule_runs(run_id);

CREATE INDEX idx_schedule_runs_schedule_id
    ON schedule_runs(schedule_id, fired_at DESC);

END;

PRAGMA foreign_keys = ON;
