-- Phase 5 / E1 (CW-20260419-0027): playbook system reflex matcher.
--
-- Adds the playbook_match_log table. Every user turn that fires a reflex
-- match is logged here with source=reflex so the E3 strategy loop and future
-- analytics can inspect the reflex runtime's decisions.
--
-- Columns:
--   id                    autoincrement primary key
--   session_id            the Chat session that triggered the match
--   turn_id               optional turn message ID (NULL when not tracked)
--   reflex_id             the Reflex.ID that matched (eg planner-mention)
--   priority              the reflex's priority at match time (snapshot)
--   source                always "reflex" for rows written by this package
--   matched_input_excerpt first 200 chars of raw user input (no PII expansion)
--   hint_tier             ScopeTier hint forwarded to AssignRole (string form)
--   hint_pattern          ExecutionPattern hint forwarded to AssignRole
--   profile_slug          agent profile slug the reflex resolved to
--   mode                  ModeSignal from reflex.SideEffects (may be empty)
--   matched_at            UTC timestamp of the match event
--
-- All CREATE ... IF NOT EXISTS so this migration is safe to replay.

CREATE TABLE IF NOT EXISTS playbook_match_log (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id            TEXT    NOT NULL,
    turn_id               TEXT,
    reflex_id             TEXT    NOT NULL,
    priority              INTEGER NOT NULL DEFAULT 0,
    source                TEXT    NOT NULL DEFAULT 'reflex',
    matched_input_excerpt TEXT    NOT NULL DEFAULT '',
    hint_tier             TEXT    NOT NULL DEFAULT '',
    hint_pattern          TEXT    NOT NULL DEFAULT '',
    profile_slug          TEXT    NOT NULL DEFAULT '',
    mode                  TEXT    NOT NULL DEFAULT '',
    matched_at            DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

-- Index on session_id for per-session log queries
CREATE INDEX IF NOT EXISTS idx_playbook_match_log_session ON playbook_match_log(session_id);

-- Index on reflex_id for reflex-level analytics
CREATE INDEX IF NOT EXISTS idx_playbook_match_log_reflex ON playbook_match_log(reflex_id);
