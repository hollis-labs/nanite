-- 008_messages_envelope_response_role.sql
-- Expand messages.role CHECK constraint to allow 'envelope_response'.
-- See plans/phase-3-s5-envelope-typed-responses.md §T5, §D5.
--
-- SQLite can't ALTER a CHECK constraint, and writable_schema edits to
-- sqlite_master don't take effect on an open connection. Full table
-- recreation is the only reliable path.
--
-- PRAGMA foreign_keys must sit OUTSIDE the BEGIN/COMMIT block: SQLite makes
-- the pragma a no-op inside an open transaction
-- (https://sqlite.org/pragma.html#pragma_foreign_keys). The migration runner
-- pins every statement to a single *sql.Conn so the PRAGMA set here carries
-- into the transaction that follows. Needed for CW-20260417-0477 — any
-- orphan FK row (bookmarks → messages, artifacts → messages, or a message
-- with a stale session_id) tripped FK enforcement on DROP/INSERT before
-- this fix.
--
-- Idempotent: CREATE ... IF NOT EXISTS on messages_new and the rename-over
-- pattern leave the messages table in the same end state regardless of
-- starting state.

PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE IF NOT EXISTS messages_new (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id),
    agent_id TEXT,
    role TEXT NOT NULL CHECK(role IN ('user','assistant','system','tool','envelope_response')),
    content TEXT NOT NULL,
    envelope TEXT,
    metadata TEXT DEFAULT '{}',
    parent_id TEXT REFERENCES messages(id),
    is_compacted BOOLEAN DEFAULT FALSE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Plain INSERT INTO so any row that can't be copied fails loudly rather than
-- being silently dropped. On second run messages_new is empty (renamed away)
-- so the INSERT never conflicts.
INSERT INTO messages_new
    SELECT id, session_id, agent_id, role, content, envelope, metadata, parent_id, is_compacted, created_at
      FROM messages;

DROP TABLE messages;

ALTER TABLE messages_new RENAME TO messages;

-- idx_messages_content from 001_schema.sql duplicated idx_messages_session
-- (same (session_id, created_at) key). The rebuild drops the duplicate and
-- we do not recreate it here.
CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, created_at);

COMMIT;

PRAGMA foreign_keys = ON;
