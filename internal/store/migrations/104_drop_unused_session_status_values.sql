-- +goose Up
-- +goose NO TRANSACTION
-- Phase 0 item 25 (TASKS/phase-0/25-drop-unused-session-status-enum.md):
-- narrow sessions.status's CHECK constraint back to
-- ('active','paused','archived'), dropping the three values
-- (sleeping/halted/terminated) added by
-- 074_agent_profiles_multi_agent.sql for an FU-28 multi-agent wake/sleep/
-- shutdown lifecycle that never got a writer -- verified two independent
-- ways that zero code path ever sets any of the three: no `UPDATE sessions`
-- statement anywhere in internal/ passes an arbitrary status value except
-- SetSessionStatusByAgentID (internal/store/agents.go), which itself has
-- zero call sites; and internal/api/sessions.go's PATCH /api/sessions/{id}
-- handler already whitelists only active/paused/archived before anything
-- reaches the store layer. See decision log Section 24 ("Cleanup targets,
-- session lifecycle") and
-- docs/engineering/architecture/06-session-lifecycle-and-recovery.md's
-- Cleanup section.
--
-- sessions.halted_at/halted_reason (internal/store/session_halt.go) are a
-- separate, real, actively-used mechanism -- the monitor-loop driver's
-- pre-tick halt check -- unrelated to this status enum despite the shared
-- English word "halted" (apparently leaked vocabulary from
-- durable_agent_instances.status, which has its own distinct 'stopped'
-- lifecycle). Both columns are untouched by this migration, copied through
-- unchanged like every other column below.
--
-- SQLite cannot narrow (or otherwise alter) a CHECK constraint in place, so
-- the table is rebuilt via the rename-recreate-copy pattern established by
-- 074_agent_profiles_multi_agent.sql (lines 44-105), the same migration
-- that originally widened this constraint. The column shape below reflects
-- the live schema as of this migration -- notably without
-- compaction_summary/compacted_at (dropped by
-- 102_drop_session_compaction_summary_fields.sql) and without intent
-- (dropped by 103_drop_session_intent.sql), confirmed against a fresh
-- `newTestStore` schema dump rather than assumed. Any future column added
-- to sessions must be re-mirrored here or in a later table-rebuild
-- migration, same caveat 074 left for itself.

PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE IF NOT EXISTS sessions_new (
    id TEXT PRIMARY KEY,
    short_code TEXT NOT NULL UNIQUE,
    title TEXT,
    custom_name TEXT,
    workspace_id TEXT REFERENCES workspaces(id),
    project_id TEXT REFERENCES projects(id),
    context_type TEXT,
    context_id TEXT,
    provider TEXT,
    model TEXT,
    status TEXT DEFAULT 'active' CHECK(status IN ('active','paused','archived')),
    is_pinned BOOLEAN DEFAULT FALSE,
    sort_order INTEGER DEFAULT 0,
    message_count INTEGER DEFAULT 0,
    tags TEXT DEFAULT '[]',
    metadata TEXT DEFAULT '{}',
    last_activity DATETIME DEFAULT CURRENT_TIMESTAMP,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    context_prompt TEXT NOT NULL DEFAULT '',
    current_mode_id TEXT REFERENCES modes(id),
    auto_switch_override INTEGER DEFAULT NULL,
    halted_at DATETIME,
    halted_reason TEXT
);

INSERT INTO sessions_new
    (id, short_code, title, custom_name, workspace_id, project_id,
     context_type, context_id, provider, model, status,
     is_pinned, sort_order, message_count, tags, metadata,
     last_activity, created_at, updated_at,
     context_prompt, current_mode_id, auto_switch_override,
     halted_at, halted_reason)
SELECT
    id, short_code, title, custom_name, workspace_id, project_id,
    context_type, context_id, provider, model, status,
    is_pinned, sort_order, message_count, tags, metadata,
    last_activity, created_at, updated_at,
    context_prompt, current_mode_id, auto_switch_override,
    halted_at, halted_reason
FROM sessions;

DROP TABLE sessions;

ALTER TABLE sessions_new RENAME TO sessions;

CREATE INDEX IF NOT EXISTS idx_sessions_workspace ON sessions(workspace_id, last_activity DESC);
CREATE INDEX IF NOT EXISTS idx_sessions_pinned ON sessions(is_pinned, sort_order);
CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions(status);

END;

PRAGMA foreign_keys = ON;

-- +goose Down
-- Rebuilds sessions with the original 6-value CHECK restored, exactly as
-- 074_agent_profiles_multi_agent.sql first defined it. Structure only --
-- no data is fabricated for the three re-widened values; existing rows
-- keep whatever status they already held, and only active/paused/archived
-- are possible pre-Down since the Up side above never allowed anything
-- else in.

PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE IF NOT EXISTS sessions_new (
    id TEXT PRIMARY KEY,
    short_code TEXT NOT NULL UNIQUE,
    title TEXT,
    custom_name TEXT,
    workspace_id TEXT REFERENCES workspaces(id),
    project_id TEXT REFERENCES projects(id),
    context_type TEXT,
    context_id TEXT,
    provider TEXT,
    model TEXT,
    status TEXT DEFAULT 'active' CHECK(status IN ('active','paused','archived','sleeping','halted','terminated')),
    is_pinned BOOLEAN DEFAULT FALSE,
    sort_order INTEGER DEFAULT 0,
    message_count INTEGER DEFAULT 0,
    tags TEXT DEFAULT '[]',
    metadata TEXT DEFAULT '{}',
    last_activity DATETIME DEFAULT CURRENT_TIMESTAMP,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    context_prompt TEXT NOT NULL DEFAULT '',
    current_mode_id TEXT REFERENCES modes(id),
    auto_switch_override INTEGER DEFAULT NULL,
    halted_at DATETIME,
    halted_reason TEXT
);

INSERT INTO sessions_new
    (id, short_code, title, custom_name, workspace_id, project_id,
     context_type, context_id, provider, model, status,
     is_pinned, sort_order, message_count, tags, metadata,
     last_activity, created_at, updated_at,
     context_prompt, current_mode_id, auto_switch_override,
     halted_at, halted_reason)
SELECT
    id, short_code, title, custom_name, workspace_id, project_id,
    context_type, context_id, provider, model, status,
    is_pinned, sort_order, message_count, tags, metadata,
    last_activity, created_at, updated_at,
    context_prompt, current_mode_id, auto_switch_override,
    halted_at, halted_reason
FROM sessions;

DROP TABLE sessions;

ALTER TABLE sessions_new RENAME TO sessions;

CREATE INDEX IF NOT EXISTS idx_sessions_workspace ON sessions(workspace_id, last_activity DESC);
CREATE INDEX IF NOT EXISTS idx_sessions_pinned ON sessions(is_pinned, sort_order);
CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions(status);

END;

PRAGMA foreign_keys = ON;
