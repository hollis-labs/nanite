-- Session-level agent config overrides (for config cascade)
CREATE TABLE IF NOT EXISTS session_agent_overrides (
    session_id TEXT NOT NULL,
    overrides  TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (session_id)
);
