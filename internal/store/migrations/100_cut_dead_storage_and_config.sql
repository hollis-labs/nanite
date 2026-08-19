-- 18a-cut-dead-storage-and-config: drop tables/columns confirmed dead per
-- docs/engineering/TASKS.md Phase 0 item 18 (TASKS/phase-0/18a-cut-dead-
-- storage-and-config.md has the full per-table verification).
--
-- Tables dropped:
--   agent_cycles              -- full CRUD (internal/store/agent_cycles.go),
--                                zero callers anywhere outside that file.
--   agent_boot_plans          -- live UI+API round-trip (Boot tab, 4 REST
--                                routes), but zero effect on any real agent
--                                boot/launch -- nothing in the launch path
--                                ever reads plant_items_json/callbacks_json.
--   workflows                 -- zero Go code reads or writes it,
--                                workflow_runs/workflow_run_steps (the real
--                                workflow-execution feature) are untouched.
--   session_agent_overrides   -- write side (SetSessionOverrides) has zero
--                                callers anywhere, the read side
--                                (GetSessionOverrides) is real, hot-path
--                                code but is a permanent no-op once the
--                                write side never fires (overridesJSON is
--                                always "{}").
--   session_stats             -- defensive DROP TABLE IF EXISTS: this table
--                                was created out-of-band by the (now
--                                deleted) session-stats builtin plugin's own
--                                InitSchema() call, not by a CREATE TABLE in
--                                this migration ledger, so installations
--                                where the plugin already loaded once may
--                                have the table even though no CREATE TABLE
--                                session_stats exists anywhere in this
--                                ledger.
--
-- Columns dropped:
--   providers.base_url, providers.api_key -- confirmed zero runtime effect
--   on request construction (never read in the provider/LLM HTTP-request-
--   building code). The real, live API-key mechanism is the OS keychain via
--   internal/secrets, wired through internal/api/provider_manage.go's
--   handleSetProviderAPIKey, which never touched this column in the first
--   place.

-- +goose Up
DROP TABLE IF EXISTS agent_cycles;
DROP TABLE IF EXISTS agent_boot_plans;
DROP TABLE IF EXISTS workflows;
DROP TABLE IF EXISTS session_agent_overrides;
DROP TABLE IF EXISTS session_stats;

ALTER TABLE providers DROP COLUMN base_url;
ALTER TABLE providers DROP COLUMN api_key;

-- +goose Down
ALTER TABLE providers ADD COLUMN base_url TEXT;
ALTER TABLE providers ADD COLUMN api_key TEXT;

CREATE TABLE IF NOT EXISTS agent_cycles (
    id                     TEXT PRIMARY KEY,
    agent_id               TEXT NOT NULL REFERENCES agent_profiles(id),
    session_id             TEXT NOT NULL DEFAULT '',
    cycle_kind             TEXT NOT NULL DEFAULT 'request',
    status                 TEXT NOT NULL DEFAULT 'running',
    input_pointer_json     TEXT NOT NULL DEFAULT '{}',
    output_summary         TEXT NOT NULL DEFAULT '',
    decisions_json         TEXT NOT NULL DEFAULT '[]',
    open_items_json        TEXT NOT NULL DEFAULT '[]',
    artifact_pointers_json TEXT NOT NULL DEFAULT '[]',
    tool_cache_refs_json   TEXT NOT NULL DEFAULT '[]',
    reboot_reason          TEXT NOT NULL DEFAULT '',
    started_at             TEXT NOT NULL DEFAULT (datetime('now')),
    ended_at               TEXT
);

CREATE TABLE IF NOT EXISTS agent_boot_plans (
    agent_id          TEXT PRIMARY KEY REFERENCES agent_profiles(id) ON DELETE CASCADE,
    schema_version    INTEGER NOT NULL DEFAULT 1,
    plant_items_json  TEXT NOT NULL DEFAULT '[]',
    callbacks_json    TEXT NOT NULL DEFAULT '[]',
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_agent_boot_plans_updated_at
    ON agent_boot_plans(updated_at);

CREATE TABLE IF NOT EXISTS workflows (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    trigger TEXT NOT NULL,
    definition TEXT NOT NULL,
    workspace_id TEXT,
    is_enabled BOOLEAN DEFAULT TRUE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS session_agent_overrides (
    session_id TEXT NOT NULL,
    overrides  TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (session_id)
);

CREATE TABLE IF NOT EXISTS session_stats (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL UNIQUE,
    message_count INTEGER DEFAULT 0,
    start_time TIMESTAMP,
    end_time TIMESTAMP,
    total_input_tokens INTEGER DEFAULT 0,
    total_output_tokens INTEGER DEFAULT 0,
    agent_switches INTEGER DEFAULT 0,
    tool_calls INTEGER DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_session_stats_session_id ON session_stats(session_id);
