-- +goose Up
-- 152_workflow_scheduled_activations.sql
-- Durable Nanite host material for Hadron wait/retry activations. Hadron owns
-- activation and wait semantics, go-scheduler owns one-shot Fire lifecycle,
-- and these tables provide the application-owned persistence boundary.

CREATE TABLE workflow_activation_schedules (
    schedule_id     TEXT PRIMARY KEY,
    activation_id   TEXT NOT NULL UNIQUE,
    activation_json TEXT NOT NULL,
    fire_at         TEXT NOT NULL,
    next_run        TEXT,
    status          TEXT NOT NULL CHECK (status IN ('active','materialized','canceled')),
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE INDEX idx_workflow_activation_schedules_due
    ON workflow_activation_schedules(status, next_run, schedule_id);

CREATE TABLE workflow_activation_fires (
    fire_id                 TEXT PRIMARY KEY,
    schedule_id             TEXT NOT NULL REFERENCES workflow_activation_schedules(schedule_id),
    scheduled_at            TEXT NOT NULL,
    fired_at                TEXT NOT NULL,
    claim_expires_at        TEXT,
    dispatch_accepted_at    TEXT,
    status                  TEXT NOT NULL CHECK (
        status IN ('pending','claimed','retrying','succeeded','skipped','exhausted')
    ),
    attempt_count           INTEGER NOT NULL DEFAULT 0,
    last_error              TEXT,
    next_attempt_at         TEXT,
    retry_max_attempts      INTEGER NOT NULL DEFAULT 0,
    retry_backoff_strategy  TEXT NOT NULL DEFAULT 'none' CHECK (
        retry_backoff_strategy IN ('none','constant','linear','exponential')
    ),
    retry_initial_delay_ns  INTEGER NOT NULL DEFAULT 0,
    retry_max_delay_ns      INTEGER NOT NULL DEFAULT 0,
    job_type                TEXT NOT NULL,
    job_payload             TEXT NOT NULL
);

CREATE INDEX idx_workflow_activation_fires_due
    ON workflow_activation_fires(status, next_attempt_at, claim_expires_at, fire_id);

CREATE TABLE workflow_wait_materializations (
    wait_id       TEXT PRIMARY KEY REFERENCES workflow_waits(wait_id),
    kind          TEXT NOT NULL,
    resume_url    TEXT NOT NULL DEFAULT '',
    expires_at    TEXT,
    status        TEXT NOT NULL CHECK (status IN ('open','resolved')),
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

CREATE INDEX idx_workflow_wait_materializations_open
    ON workflow_wait_materializations(status, expires_at, wait_id);

-- +goose Down

DROP TABLE IF EXISTS workflow_wait_materializations;
DROP TABLE IF EXISTS workflow_activation_fires;
DROP TABLE IF EXISTS workflow_activation_schedules;
