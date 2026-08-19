-- +goose Up
-- Phase 0 item 21 (TASKS/phase-0/21-cut-modes.md): "Cut Modes, in full".
-- Drops all four pieces of the Modes system enumerated in
-- docs/engineering/TASKS.md and docs/engineering/architecture/03-steering.md's
-- "What's cut" section:
--   1. Session Mode        -- the `modes` table + sessions.current_mode_id.
--   2. Legacy Agent Mode   -- the `agent_modes` table (older, per-agent,
--                            predates `modes`/current_mode_id).
--   3. Mode<->agent junction -- `agent_mode_assignments`.
--   4. classify.ClassifyMode -- a Go-only cut, no schema footprint (deleted
--                            in the same commit; see chat_broker_dispatch.go
--                            and chat_generate.go).
--
-- Scope correction from this task's own investigation (per
-- EXECUTION-PROCESS.md worker step 7 -- the decided action stands, this
-- just documents why the migration is bigger than the task file's own
-- "Touches" list named): auditing every Go call site touching store.Mode /
-- store.AgentMode surfaced two more schema objects that exist ONLY to serve
-- Session Mode / its F2 auto-switch preference layer, with zero remaining
-- purpose once current_mode_id and classify.ClassifyMode are gone:
--   - sessions.auto_switch_override (045_session_auto_switch_override.sql,
--     F2 CW-20260429-0002) -- the per-session override for
--     user_settings.mode_auto_switch_pref; its only reader was the deleted
--     PATCH /api/sessions/{id}/auto-switch handler.
--   - user_settings.mode_auto_switch_pref (041_user_settings_mode_auto_switch.sql,
--     B3 CW-20260428-0011) -- gated auto-applying the mode_suggestion SSE
--     event that classify.ClassifyMode used to emit; that event is gone.
-- Both are dropped here alongside the four enumerated pieces. See this
-- task's Work Log in TASKS/phase-0/21-cut-modes.md for the full trail.
--
-- Drop order matters under this codebase's DSN-level `PRAGMA foreign_keys =
-- ON` (internal/store/store.go): every FK reference INTO the `modes` table
-- must be removed before `modes` itself is dropped, or SQLite raises
-- "FOREIGN KEY constraint failed" on the DROP TABLE (verified live against
-- the sqlite3 3.43.2 CLI -- the same DROP-COLUMN/DROP-TABLE code path
-- modernc.org/sqlite v1.54.0 / SQLITE_VERSION 3.53.3 embeds, matching
-- migration 103's own verification approach):
--   1. agent_mode_assignments (mode_id REFERENCES modes(id) ON DELETE CASCADE)
--      -- drop the whole table first.
--   2. sessions.current_mode_id (TEXT REFERENCES modes(id)) -- drop the
--      column; SQLite permits DROP COLUMN on a column-level FK that refers
--      to no other column, same rule migration 103 confirmed for CHECK
--      constraints.
--   3. agent_modes -- no FK to modes (a separate, older, unrelated table);
--      order relative to modes doesn't matter, dropped here for locality
--      with its own junction/session concerns.
--   4. modes -- now unreferenced, safe to drop.
-- sessions.auto_switch_override and user_settings.mode_auto_switch_pref
-- carry no FK and can drop in any order; placed last.
--
-- IMPORTANT: no semicolons inside comments.

DROP TABLE agent_mode_assignments;

ALTER TABLE sessions DROP COLUMN current_mode_id;

DROP TABLE agent_modes;

DROP TABLE modes;

ALTER TABLE sessions DROP COLUMN auto_switch_override;

ALTER TABLE user_settings DROP COLUMN mode_auto_switch_pref;

-- +goose Down
-- Re-creates the five dropped schema objects with their exact original
-- shape (001_schema.sql for the three tables; 040/041/045 for the three
-- columns). Structure only, not data -- DROP TABLE/COLUMN is inherently
-- lossy and nothing in this codebase read any of these back into a durable
-- log, so empty tables / NULL-or-default-filled columns of the correct
-- shape are the honest, achievable Down here, matching 102's and 103's Down
-- precedent. Re-creation order is the reverse of the Up section so every FK
-- target exists before the column/table that references it is re-added.

CREATE TABLE modes (
    id TEXT PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    prompt_addendum TEXT NOT NULL DEFAULT '',
    tool_overrides TEXT NOT NULL DEFAULT '{}',
    settings TEXT NOT NULL DEFAULT '{}',
    is_builtin INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE agent_modes (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    slug TEXT NOT NULL,
    name TEXT NOT NULL,
    prompt_addendum TEXT NOT NULL,
    tool_overrides TEXT DEFAULT '{}',
    settings TEXT DEFAULT '{}',
    UNIQUE(agent_id, slug)
);

ALTER TABLE sessions ADD COLUMN current_mode_id TEXT REFERENCES modes(id);

CREATE TABLE agent_mode_assignments (
    agent_id TEXT NOT NULL,
    mode_id TEXT NOT NULL REFERENCES modes(id) ON DELETE CASCADE,
    PRIMARY KEY (agent_id, mode_id)
);

ALTER TABLE sessions ADD COLUMN auto_switch_override INTEGER DEFAULT NULL;

ALTER TABLE user_settings ADD COLUMN mode_auto_switch_pref TEXT NOT NULL DEFAULT '';
