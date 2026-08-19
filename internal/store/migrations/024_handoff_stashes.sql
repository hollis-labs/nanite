-- +goose Up
-- CW-20260420-0024 — handoff_stashes: pre-compaction structured session-state blob (P7 HandoffStash)
--
-- Stores JSON payloads (decisions_locked, open_questions, active_file_refs,
-- active_ticket_ids, should_reread) keyed by session — retrieved post-compaction
-- via stash_id.
--
-- ON DELETE CASCADE evicts stashes when the parent session is removed.

CREATE TABLE IF NOT EXISTS handoff_stashes (
    id         TEXT NOT NULL PRIMARY KEY,
    session_id TEXT NOT NULL,
    payload    TEXT NOT NULL,
    created_at TEXT NOT NULL,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_handoff_stashes_session
    ON handoff_stashes(session_id, created_at DESC);

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
