-- +goose Up
-- Phase 0 item 20 (TASKS/phase-0/20-retire-workspaces-and-instance-mechanism.md):
-- second of the four workspaces-retirement migrations (106 dropped
-- sessions.workspace_id; this one drops projects.workspace_id; 108 drops
-- workspace_role_trust; 109 drops the workspaces table itself, last).
--
-- IMPORTANT — disambiguation this task's own investigation required (see
-- the task file's Context section in full): `projects` is NOT dropped.
-- It is simultaneously (1) the UI-grouping table formerly nested under
-- `workspaces`, and (2) the FK target of `agent_projects` — the live
-- Agent Construction scope mechanism
-- (docs/engineering/architecture/01-agent-construction.md). Only the
-- workspace_id column + its FK to the now-being-retired `workspaces`
-- table is dropped here. `projects` and `agent_projects` (and every row
-- in both) are otherwise untouched — this is a column drop, not a table
-- drop.
--
-- No index references projects.workspace_id (verified against a
-- fresh-migrated store's sqlite_master — only the PK's automatic index
-- exists), so this is a bare DROP COLUMN, no rebuild needed. The column
-- is NOT NULL with an inline FK; neither restriction blocks DROP COLUMN
-- in SQLite (only PK membership, UNIQUE, and index membership do).

ALTER TABLE projects DROP COLUMN workspace_id;

-- +goose Down
-- Re-adds projects.workspace_id with its original NOT NULL + FK shape
-- (migrations/001_schema.sql). Structure only — DROP COLUMN is inherently
-- lossy, so every existing project's workspace_id round-trips as the
-- placeholder 'default' (the sole real workspace ID per migration 088's
-- consolidation) rather than left NULL, since the column is NOT NULL and
-- existing rows must satisfy the constraint being restored. Requires the
-- workspaces table (and a 'default' row in it) to exist — true when
-- reversing a full downgrade, since 109's Down (which also reseeds the
-- 'default' row) runs before this one.

ALTER TABLE projects ADD COLUMN workspace_id TEXT NOT NULL REFERENCES workspaces(id) DEFAULT 'default';
