-- +goose Up
-- +goose NO TRANSACTION
-- 082_durable_agent_stopped_status.sql
-- Add the durable-agent stopped lifecycle status.
--
-- SQLite cannot widen a CHECK constraint in place, so rebuild the instance
-- table with the same columns plus stopped in the status vocabulary.

PRAGMA foreign_keys = OFF;

DROP TABLE IF EXISTS durable_agent_instances_new;

BEGIN;

CREATE TABLE durable_agent_instances_new (
    id                 TEXT PRIMARY KEY,
    name               TEXT NOT NULL,
    slug               TEXT NOT NULL UNIQUE,
    profile_id         TEXT NOT NULL REFERENCES agent_profiles(id),
    lifecycle_class    TEXT NOT NULL DEFAULT 'advisor'
        CHECK(lifecycle_class IN ('advisor','process','template','harness')),
    provider           TEXT NOT NULL DEFAULT '',
    model              TEXT NOT NULL DEFAULT '',
    runtime_kind       TEXT NOT NULL DEFAULT 'api',
    launch_source_type TEXT NOT NULL DEFAULT 'durable_advisor'
        CHECK(launch_source_type IN ('api_chat','cli_harness','boot_profile','durable_advisor','process_tick','task_template_run')),
    launch_source_id   TEXT NOT NULL DEFAULT '',
    work_root          TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL DEFAULT 'sleeping'
        CHECK(status IN ('sleeping','starting','active','paused','stopped','start_requested','stop_requested','resume_requested','failed','archived')),
    current_session_id TEXT NOT NULL DEFAULT '',
    failure_reason     TEXT NOT NULL DEFAULT '',
    metadata_json      TEXT NOT NULL DEFAULT '{}',
    created_at         TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at         TEXT NOT NULL DEFAULT (datetime('now')),
    archived_at        TEXT
);

INSERT INTO durable_agent_instances_new
    (id, name, slug, profile_id, lifecycle_class, provider, model,
     runtime_kind, launch_source_type, launch_source_id, work_root, status,
     current_session_id, failure_reason, metadata_json,
     created_at, updated_at, archived_at)
SELECT
    id, name, slug, profile_id, lifecycle_class, provider, model,
    runtime_kind, launch_source_type, launch_source_id, work_root, status,
    current_session_id, failure_reason, metadata_json,
    created_at, updated_at, archived_at
FROM durable_agent_instances;

DROP TABLE durable_agent_instances;
ALTER TABLE durable_agent_instances_new RENAME TO durable_agent_instances;

CREATE INDEX IF NOT EXISTS idx_durable_agent_instances_profile
    ON durable_agent_instances(profile_id);

CREATE INDEX IF NOT EXISTS idx_durable_agent_instances_status
    ON durable_agent_instances(status, updated_at DESC);

END;

PRAGMA foreign_keys = ON;

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
