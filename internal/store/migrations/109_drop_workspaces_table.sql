-- +goose Up
-- Phase 0 item 20 (TASKS/phase-0/20-retire-workspaces-and-instance-mechanism.md):
-- last of the four workspaces-retirement migrations. The in-app
-- `workspaces` table (internal/store/migrations/001_schema.sql) is a real,
-- 1-row table (migration 088_consolidate_personal_workspace.sql's own
-- commit message: consolidated a second "personal" workspace down to the
-- sole "default" one) that gated nothing load-bearing once its three real
-- dependents are gone:
--   - sessions.workspace_id (dropped by 106)
--   - projects.workspace_id + FK (dropped by 107) — `projects` itself and
--     `agent_projects` (the live Agent Construction scope mechanism,
--     docs/engineering/architecture/01-agent-construction.md) are
--     untouched; only the workspace nesting is gone.
--   - workspace_role_trust (dropped by 108, operator-confirmed full
--     removal)
--
-- This migration must run last (106-108 first) — dropping `workspaces`
-- before its dependents' FK columns/tables are gone would leave dangling
-- FK references. By the time this runs, nothing in the schema references
-- workspaces(id) anymore, so a bare DROP TABLE is sufficient.
--
-- The Go-side Workspace CRUD (internal/store/workspaces.go),
-- ResolveWorkspaceID, the 5 REST routes (internal/api/workspaces.go,
-- internal/api/api.go), and the settings-page workspace switcher
-- (ui/src/components/settings/WorkspaceProjectManager.tsx, simplified to a
-- project-only manager) are removed in the same task, not by this
-- migration alone.

DROP TABLE IF EXISTS workspaces;

-- +goose Down
-- Recreates the workspaces table with its original shape
-- (migrations/001_schema.sql) and reseeds the single 'default' row that
-- migration 088's consolidation left as the sole real workspace — the
-- same row 107's Down and 108's Down (transitively, via projects.workspace_id
-- and workspace_role_trust's FK) depend on existing. Structure +
-- the one real seed row; no other workspace data is recoverable (DROP
-- TABLE is inherently lossy), which is honest — 088 had already
-- consolidated everything down to this one row before this migration
-- ever runs.

CREATE TABLE IF NOT EXISTS workspaces (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    icon TEXT,
    sort_order INTEGER DEFAULT 0,
    settings TEXT DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO workspaces (id, name, description, icon, sort_order, settings, created_at, updated_at)
VALUES ('default', 'Default', '', '', 0, '{}', datetime('now'), datetime('now'));
