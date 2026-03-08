CREATE TABLE IF NOT EXISTS prompt_templates (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    slug       TEXT NOT NULL UNIQUE,
    scope      TEXT NOT NULL CHECK(scope IN ('system','mode','skill','context')),
    template   TEXT NOT NULL,
    variables  TEXT NOT NULL DEFAULT '[]',
    priority   INTEGER NOT NULL DEFAULT 0,
    is_builtin INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_prompt_templates (
    agent_id    TEXT NOT NULL REFERENCES agent_profiles(id) ON DELETE CASCADE,
    template_id TEXT NOT NULL REFERENCES prompt_templates(id) ON DELETE CASCADE,
    PRIMARY KEY (agent_id, template_id)
);
