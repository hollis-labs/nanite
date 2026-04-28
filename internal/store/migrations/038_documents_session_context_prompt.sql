-- J10 (CW-20260426-0008): documents table + session context prompt.
-- documents: user-uploaded/pasted documents persisted across sessions.
-- session_context_prompt: user-authored prompt block scoped to one session,
-- survives compaction as a pinned context slot (SlotUserContext).
-- IMPORTANT: no semicolons inside comments (splitSQL naive-split limitation).

CREATE TABLE IF NOT EXISTS documents (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL DEFAULT '',
    name        TEXT NOT NULL,
    mime_type   TEXT NOT NULL DEFAULT 'text/plain',
    content     TEXT NOT NULL DEFAULT '',
    size_bytes  INTEGER NOT NULL DEFAULT 0,
    included    INTEGER NOT NULL DEFAULT 0,
    full_content INTEGER NOT NULL DEFAULT 0,
    summary     TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_documents_session
    ON documents (session_id, created_at DESC);

ALTER TABLE sessions
    ADD COLUMN context_prompt TEXT NOT NULL DEFAULT '';
