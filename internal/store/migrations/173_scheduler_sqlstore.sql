-- +goose Up
-- Reference DDL from released go-scheduler v0.3.0, owned by Nanite goose.
-- Shared schema is managed by Nanite's migration chain, not library Migrate.
-- Timestamps are UTC, fixed-width text
-- (2006-01-02T15:04:05.000000000Z) so lexicographic order equals time order
-- and nanosecond precision round-trips exactly for compare-and-swap.

CREATE TABLE IF NOT EXISTS gosched_schedules (
    id         TEXT PRIMARY KEY,
    cron_expr  TEXT    NOT NULL DEFAULT '',
    last_run   TEXT    NOT NULL,
    next_run   TEXT    NOT NULL,
    enabled    INTEGER NOT NULL DEFAULT 1,
    job_type   TEXT    NOT NULL DEFAULT '',
    payload    BLOB,
    retry_json TEXT    NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS gosched_schedules_due
    ON gosched_schedules (enabled, next_run);

CREATE TABLE IF NOT EXISTS gosched_fires (
    id               TEXT PRIMARY KEY,
    schedule_id      TEXT    NOT NULL,
    scheduled_at     TEXT    NOT NULL,
    fired_at         TEXT    NOT NULL,
    claim_expires_at TEXT    NOT NULL,
    attempt          INTEGER NOT NULL DEFAULT 0,
    status           TEXT    NOT NULL,
    next_attempt_at  TEXT    NOT NULL,
    last_error       TEXT    NOT NULL DEFAULT '',
    retry_json       TEXT    NOT NULL DEFAULT '{}',
    job_type         TEXT    NOT NULL DEFAULT '',
    payload          BLOB
);

CREATE INDEX IF NOT EXISTS gosched_fires_due
    ON gosched_fires (status, next_attempt_at);

-- Host identities are explicit, rather than inferred from schedule ID prefixes.
CREATE TABLE scheduler_schedule_identity (
    schedule_id TEXT PRIMARY KEY REFERENCES gosched_schedules(id),
    family TEXT NOT NULL CHECK (family IN ('agent','workflow')),
    source_id TEXT NOT NULL,
    UNIQUE(family, source_id)
);

CREATE TABLE scheduler_fire_identity (
    fire_id TEXT PRIMARY KEY REFERENCES gosched_fires(id),
    schedule_id TEXT NOT NULL REFERENCES scheduler_schedule_identity(schedule_id),
    family TEXT NOT NULL CHECK (family IN ('agent','workflow')),
    legacy_row_id TEXT NOT NULL,
    UNIQUE(family, legacy_row_id)
);
CREATE INDEX scheduler_fire_identity_schedule ON scheduler_fire_identity(schedule_id);

CREATE TABLE scheduler_dispatch_receipts (
    fire_id TEXT PRIMARY KEY REFERENCES gosched_fires(id),
    accepted_at TEXT NOT NULL
);

-- Empty until the offline converter commits the complete validated copy.
CREATE TABLE scheduler_storage_authority (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    authority TEXT NOT NULL,
    migrated_at TEXT NOT NULL
);

-- Projection triggers are installed by the offline converter AFTER copying
-- history. Installing them here would count historical materialization twice.

-- +goose Down
-- Refuse a destructive rollback once any scheduler material exists, including
-- an authority marker. Restore the pre-cutover backup only before new work.
-- +goose StatementBegin
CREATE TEMP TABLE scheduler_down_guard (empty INTEGER CHECK(empty = 0));
INSERT INTO scheduler_down_guard
SELECT (SELECT COUNT(*) FROM gosched_schedules) +
       (SELECT COUNT(*) FROM gosched_fires) +
       (SELECT COUNT(*) FROM scheduler_storage_authority);
DROP TABLE scheduler_down_guard;
-- +goose StatementEnd
DROP TABLE scheduler_dispatch_receipts;
DROP TABLE scheduler_fire_identity;
DROP TABLE scheduler_schedule_identity;
DROP TABLE scheduler_storage_authority;
DROP TABLE gosched_fires;
DROP TABLE gosched_schedules;
