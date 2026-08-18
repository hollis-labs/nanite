-- +goose Up
-- J11 (CW-20260426-0009): reminders + pinned_content tables.
-- reminders: agent-set deterministic triggers that inject text into future turns.
-- pinned_content: agent-pinned content surfaced in SlotUserContext.
-- IMPORTANT: no semicolons inside comments (splitSQL naive-split limitation).
--
-- Trigger v1 shapes (stored as JSON in the trigger_json column):
--   time-based:       {"type":"time","at":"<RFC3339>"}
--   turn-count-based: {"type":"turn_count","n":5}
-- Keyword-mention deferred to follow-up (followups_j11_keyword_mention_trigger).
--
-- Pin scope hierarchy:
--   turn        - cleared after current turn (ephemeral, not persisted here)
--   session     - cleared at session end (session_id non-null, cleared on session close)
--   cross_session - persists until explicit unpin (session_id NULL)
--
-- Pinned content budget: rides in SlotUserContext (2000-token shared budget).
-- Oldest pinned items truncate first when over budget.

CREATE TABLE IF NOT EXISTS reminders (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL,
    text        TEXT NOT NULL DEFAULT '',
    trigger_json TEXT NOT NULL DEFAULT '{}',
    fired_at    TEXT,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_reminders_session
    ON reminders (session_id, created_at DESC);

CREATE TABLE IF NOT EXISTS pinned_content (
    id          TEXT PRIMARY KEY,
    session_id  TEXT,
    scope       TEXT NOT NULL DEFAULT 'session',
    content     TEXT NOT NULL DEFAULT '',
    agent_id    TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_pinned_content_session
    ON pinned_content (session_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_pinned_content_cross_session
    ON pinned_content (scope, created_at DESC);

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
