-- 083_durable_agent_events.sql
-- Durable-agent scoped lifecycle activity trail.

CREATE TABLE IF NOT EXISTS durable_agent_events (
    id              TEXT PRIMARY KEY,
    instance_id     TEXT NOT NULL REFERENCES durable_agent_instances(id) ON DELETE CASCADE,
    event_type      TEXT NOT NULL,
    status_before   TEXT NOT NULL DEFAULT '',
    status_after    TEXT NOT NULL DEFAULT '',
    session_id      TEXT NOT NULL DEFAULT '',
    source          TEXT NOT NULL DEFAULT 'api',
    message         TEXT NOT NULL DEFAULT '',
    metadata_json   TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_durable_agent_events_instance_created
    ON durable_agent_events(instance_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_durable_agent_events_type_created
    ON durable_agent_events(event_type, created_at DESC);
