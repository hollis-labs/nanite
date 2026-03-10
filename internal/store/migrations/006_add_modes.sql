CREATE TABLE IF NOT EXISTS modes (
    id              TEXT PRIMARY KEY,
    slug            TEXT NOT NULL UNIQUE,
    name            TEXT NOT NULL,
    prompt_addendum TEXT NOT NULL DEFAULT '',
    tool_overrides  TEXT NOT NULL DEFAULT '{}',
    settings        TEXT NOT NULL DEFAULT '{}',
    is_builtin      INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_mode_assignments (
    agent_id TEXT NOT NULL REFERENCES agent_profiles(id) ON DELETE CASCADE,
    mode_id  TEXT NOT NULL REFERENCES modes(id) ON DELETE CASCADE,
    PRIMARY KEY (agent_id, mode_id)
);
