-- 085_agent_boot_plans.sql
--
-- Phase 25: first-class, versioned per-agent boot planting and callback
-- configuration. This stays separate from agent_profiles.directories so
-- the admin/API surface can evolve without overloading legacy JSON.
--
-- The migration runner replays every file on every boot. CREATE TABLE /
-- CREATE INDEX IF NOT EXISTS keep this migration idempotent.

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
