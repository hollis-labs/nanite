-- +goose Up
-- +goose NO TRANSACTION
-- 081_durable_agent_launch_policy.sql
-- Phase 6 durable-agent launch policy state.
--
-- The migration runner has no schema ledger, so this file is written to be
-- rerunnable. SQLite cannot widen CHECK constraints in place, so the two
-- durable-agent tables are rebuilt with the Phase 6 status and relation
-- vocabulary. Later reruns may already contain the Phase 7 "stopped" status,
-- so the rebuilt CHECK must tolerate that value too. Comments avoid semicolons
-- because splitSQL is semicolon based.

PRAGMA foreign_keys = OFF;

-- current_session_id/failure_reason were originally added here via ALTER
-- TABLE ADD COLUMN, back when migration 081 (080 pre-goose-renumbering)
-- created durable_agent_instances without them. 081's CREATE TABLE has
-- since been edited to declare both columns directly (see its Done-means
-- history), which made these two ALTER statements a no-op duplicate-column
-- error on every single boot — including a genuinely fresh install. That
-- was invisible under the old runner because "duplicate column" was
-- unconditionally swallowed there; goose's stricter Up() surfaces it as a
-- hard failure instead. Removed as dead: the CREATE TABLE below already
-- declares both columns (lines matching 081's), so the end-state schema is
-- identical with or without these two statements. See
-- 09-adopt-goose-migrations's Work Log.

BEGIN;

CREATE TABLE IF NOT EXISTS durable_agent_instances_new (
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

CREATE TABLE IF NOT EXISTS durable_agent_instance_sessions_new (
    instance_id TEXT NOT NULL REFERENCES durable_agent_instances(id) ON DELETE CASCADE,
    session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    relation    TEXT NOT NULL DEFAULT 'owned'
        CHECK(relation IN ('owned','attached','spawned','primary','run','wake','harness')),
    attached_at TEXT NOT NULL DEFAULT (datetime('now')),
    detached_at TEXT,
    PRIMARY KEY(instance_id, session_id)
);

INSERT INTO durable_agent_instance_sessions_new
    (instance_id, session_id, relation, attached_at, detached_at)
SELECT instance_id, session_id, relation, attached_at, detached_at
FROM durable_agent_instance_sessions;

DROP TABLE durable_agent_instance_sessions;
ALTER TABLE durable_agent_instance_sessions_new RENAME TO durable_agent_instance_sessions;

CREATE INDEX IF NOT EXISTS idx_durable_agent_instance_sessions_session
    ON durable_agent_instance_sessions(session_id);

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
