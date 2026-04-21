CREATE TABLE IF NOT EXISTS handoff_stashes (
    id         TEXT NOT NULL PRIMARY KEY,
    session_id TEXT NOT NULL,
    payload    TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_handoff_stashes_session
    ON handoff_stashes(session_id, created_at DESC);
