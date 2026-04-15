-- 008_messages_envelope_response_role.sql
-- Expand messages.role CHECK constraint to allow 'envelope_response'.
-- See plans/phase-3-s5-envelope-typed-responses.md §T5, §D5.
--
-- SQLite can't ALTER a CHECK constraint, and writable_schema edits to
-- sqlite_master don't take effect on an open connection. Full table
-- recreation is the only reliable path.
--
-- The BEGIN/END wrapper keeps the whole block as one statement so splitSQL
-- in store.go hands it to a single db.Exec call. That preserves connection
-- affinity so PRAGMA foreign_keys=OFF applies to the DROP/RENAME that follows.
--
-- Idempotent: CREATE ... IF NOT EXISTS on messages_new and INSERT OR IGNORE
-- both no-op on second run. The messages table always ends with the expanded
-- CHECK regardless of starting state.

BEGIN;

PRAGMA foreign_keys = OFF;

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

INSERT OR IGNORE INTO messages_new
    SELECT id, session_id, agent_id, role, content, envelope, metadata, parent_id, is_compacted, created_at
      FROM messages;

DROP TABLE messages;

ALTER TABLE messages_new RENAME TO messages;

CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_messages_content ON messages(session_id, created_at);

PRAGMA foreign_keys = ON;

END;
