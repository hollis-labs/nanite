-- +goose Up
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

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
