-- +goose Up
-- 080_durable_agent_instances.sql
-- Phase 5 durable-agent backend control plane.
--
-- agent_profiles remains the persona/template source of truth. This table
-- records configured durable agent instances with immutable provider/model/
-- runtime/start fields captured at instance creation time.

CREATE TABLE IF NOT EXISTS durable_agent_instances (
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
        CHECK(status IN ('sleeping','starting','active','paused','start_requested','stop_requested','resume_requested','failed','archived')),
    current_session_id TEXT NOT NULL DEFAULT '',
    failure_reason     TEXT NOT NULL DEFAULT '',
    metadata_json      TEXT NOT NULL DEFAULT '{}',
    created_at         TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at         TEXT NOT NULL DEFAULT (datetime('now')),
    archived_at        TEXT
);

CREATE INDEX IF NOT EXISTS idx_durable_agent_instances_profile
    ON durable_agent_instances(profile_id);

CREATE INDEX IF NOT EXISTS idx_durable_agent_instances_status
    ON durable_agent_instances(status, updated_at DESC);

CREATE TABLE IF NOT EXISTS durable_agent_instance_sessions (
    instance_id TEXT NOT NULL REFERENCES durable_agent_instances(id) ON DELETE CASCADE,
    session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    relation    TEXT NOT NULL DEFAULT 'owned'
        CHECK(relation IN ('owned','attached','spawned','primary','run','wake','harness')),
    attached_at TEXT NOT NULL DEFAULT (datetime('now')),
    detached_at TEXT,
    PRIMARY KEY(instance_id, session_id)
);

CREATE INDEX IF NOT EXISTS idx_durable_agent_instance_sessions_session
    ON durable_agent_instance_sessions(session_id);

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
