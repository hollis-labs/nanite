-- +goose Up
-- 078_agent_context_policy_and_cycles.sql
-- Durable agents: make disposable context cycles first-class.
--
-- context_policy is an authored JSON object on agent_profiles. It describes
-- how aggressively a profile should reboot or prune live model context while
-- preserving durable state elsewhere.
--
-- agent_cycles records each work cycle as the durable unit of continuity.
-- The full transcript remains in messages, session_events, and tool caches.
-- This table stores the compact boot-relevant state that future cycles
-- should read first.

ALTER TABLE agent_profiles ADD COLUMN context_policy TEXT NOT NULL DEFAULT '{}';

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

CREATE INDEX IF NOT EXISTS idx_agent_cycles_agent_started
    ON agent_cycles(agent_id, started_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_cycles_session_started
    ON agent_cycles(session_id, started_at DESC);

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
