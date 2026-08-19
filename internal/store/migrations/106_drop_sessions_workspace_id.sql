-- +goose Up
-- Phase 0 item 20 (TASKS/phase-0/20-retire-workspaces-and-instance-mechanism.md):
-- retire the in-app `workspaces` table in full. sessions.workspace_id is
-- the first of the three columns/tables this task drops (this migration;
-- 107 drops projects.workspace_id; 108 drops workspace_role_trust; 109
-- drops the workspaces table itself, last, once nothing references it).
--
-- There has only ever been one real workspace in practice (migration
-- 088_consolidate_personal_workspace.sql's own commit message: 22 real
-- sessions consolidated onto the sole "default" workspace). list/search
-- sessions endpoints (internal/api/sessions.go, internal/api/search.go)
-- drop their workspace_id query param and scope accordingly — there was
-- never anything to actually scope by.
--
-- idx_sessions_workspace must be dropped before the column — SQLite's
-- ALTER TABLE DROP COLUMN refuses to drop a column that's part of an
-- index. No rebuild needed otherwise: dropping a column with an inline
-- REFERENCES clause (but no CHECK, no UNIQUE, not part of the primary
-- key) is a bare DROP COLUMN, same precedent as
-- 102_drop_session_compaction_summary_fields.sql /
-- 103_drop_session_intent.sql.

DROP INDEX IF EXISTS idx_sessions_workspace;

ALTER TABLE sessions DROP COLUMN workspace_id;

-- +goose Down
-- Re-adds sessions.workspace_id with its original nullable-TEXT-plus-FK
-- shape (migrations/001_schema.sql) and recreates idx_sessions_workspace.
-- Structure only, not data — DROP COLUMN is inherently lossy. Requires the
-- workspaces table to exist (it will, when reversing a full downgrade —
-- goose runs Downs in descending migration order, so 109's Down recreates
-- workspaces before this one runs).

ALTER TABLE sessions ADD COLUMN workspace_id TEXT REFERENCES workspaces(id);

CREATE INDEX IF NOT EXISTS idx_sessions_workspace ON sessions(workspace_id, last_activity DESC);
