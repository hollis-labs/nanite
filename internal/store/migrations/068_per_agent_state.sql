-- Per-agent durable state — the substrate for FU-2..FU-7 of the
-- durable-agent-runtime follow-ups.
--
-- All tables are sharded by agent_id and reference agent_profiles(id).
-- They live in the central agridd DB for now. A future per-agent-file
-- backend can swap in behind the AgentStateStore interface without any
-- migration churn here.
--
-- The migration runner has no schema_migrations table, so every
-- migration re-runs on every boot. All DDL here is idempotent via
-- IF NOT EXISTS. NOTE: the runner splits statements on the semicolon
-- character, including inside comments, so this file uses none.
--
-- Tables:
--   - agent_known_tools     per-agent tool roster with activation telemetry
--   - agent_known_skills    same shape, swapping tool_name for skill_name
--   - agent_procedures      named procedure bodies (agent or shared scope)
--   - agent_log             append-only per-agent log (passes, lessons, etc)
--   - agent_knowledge_seed  manifest of memory_keys to seed into Tesseract

CREATE TABLE IF NOT EXISTS agent_known_tools (
    agent_id         TEXT NOT NULL REFERENCES agent_profiles(id),
    tool_name        TEXT NOT NULL,
    pinned           INTEGER NOT NULL DEFAULT 0,
    activation_count INTEGER NOT NULL DEFAULT 0,
    last_used_at     TEXT,
    added_at         TEXT NOT NULL DEFAULT (datetime('now')),
    ttl_seconds      INTEGER,
    reason           TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (agent_id, tool_name)
);

CREATE INDEX IF NOT EXISTS idx_agent_known_tools_agent_pinned_last
    ON agent_known_tools(agent_id, pinned DESC, last_used_at DESC);

CREATE TABLE IF NOT EXISTS agent_known_skills (
    agent_id         TEXT NOT NULL REFERENCES agent_profiles(id),
    skill_name       TEXT NOT NULL,
    pinned           INTEGER NOT NULL DEFAULT 0,
    activation_count INTEGER NOT NULL DEFAULT 0,
    last_used_at     TEXT,
    added_at         TEXT NOT NULL DEFAULT (datetime('now')),
    ttl_seconds      INTEGER,
    reason           TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (agent_id, skill_name)
);

CREATE INDEX IF NOT EXISTS idx_agent_known_skills_agent_pinned_last
    ON agent_known_skills(agent_id, pinned DESC, last_used_at DESC);

CREATE TABLE IF NOT EXISTS agent_procedures (
    agent_id   TEXT NOT NULL REFERENCES agent_profiles(id),
    name       TEXT NOT NULL,
    body       TEXT NOT NULL,
    scope      TEXT NOT NULL DEFAULT 'agent',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (agent_id, name)
);

CREATE TABLE IF NOT EXISTS agent_log (
    id         TEXT PRIMARY KEY,
    agent_id   TEXT NOT NULL REFERENCES agent_profiles(id),
    session_id TEXT NOT NULL DEFAULT '',
    ts         TEXT NOT NULL DEFAULT (datetime('now')),
    kind       TEXT NOT NULL DEFAULT '',
    entry      TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_agent_log_agent_ts
    ON agent_log(agent_id, ts);

CREATE TABLE IF NOT EXISTS agent_knowledge_seed (
    agent_id    TEXT NOT NULL REFERENCES agent_profiles(id),
    seed_key    TEXT NOT NULL,
    namespace   TEXT NOT NULL,
    body        TEXT NOT NULL,
    tags_json   TEXT NOT NULL DEFAULT '[]',
    applied_at  TEXT,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (agent_id, seed_key)
);
