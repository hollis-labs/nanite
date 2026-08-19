-- +goose Up
-- +goose NO TRANSACTION
-- 106_add_consumers_table.sql
-- TASKS/phase-1/03-add-consumers-table.md
--
-- Adds a minimal `consumers` table for ownership/tenancy tagging (see
-- docs/engineering/architecture/01-agent-construction.md's "Ownership and
-- instancing" section and GLOSSARY.md's Consumer entry) plus
-- agent_profiles.consumer_id, a nullable FK identifying which external
-- system an agent belongs to. NULL means internal/operator-owned (decision
-- log Section 4).
--
-- This is a lightweight shared-DB tag only, not a per-tenant isolated
-- database mechanism -- see
-- docs/engineering/architecture/05-storage-and-migrations.md ("A
-- separate-DB-per-instance mechanism and consumer_id tagging ... solve
-- different problems and are not steps on the same path").
--
-- Seeds one real row for Loom -- the first real external consumer named in
-- the architecture doc. No agent_profiles row is backfilled with this
-- consumer_id here; tagging Curator/Weaver/etc. is
-- 10-data-migrate-nanite-agents-md.md's job, and that task is currently
-- out-of-scope for Phase 1 (operator decision, see TASKS/phase-1/
-- 10-data-migrate-nanite-agents-md.md and phase-1-execution's planning
-- commit) -- so this migration only creates the table and seeds the one
-- consumer row, tagging no agent.
--
-- Plain CREATE TABLE + ADD COLUMN (no CHECK constraint involved), so no
-- table-rewrite dance is needed here -- see migrations 065/067 for the
-- rewrite pattern this deliberately avoids.

CREATE TABLE IF NOT EXISTS consumers (
    id TEXT PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

ALTER TABLE agent_profiles ADD COLUMN consumer_id TEXT REFERENCES consumers(id);

INSERT OR IGNORE INTO consumers (id, slug, name, created_at)
VALUES ('blt-loom-001', 'loom', 'Loom', datetime('now'));

-- +goose Down
-- Reverses in the opposite order of Up: drop the column that references
-- consumers before dropping consumers itself.
ALTER TABLE agent_profiles DROP COLUMN consumer_id;

DROP TABLE IF EXISTS consumers;
