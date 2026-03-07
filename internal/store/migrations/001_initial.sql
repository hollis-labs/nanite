CREATE TABLE IF NOT EXISTS workspaces (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    icon TEXT,
    sort_order INTEGER DEFAULT 0,
    settings TEXT DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS projects (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id),
    name TEXT NOT NULL,
    description TEXT,
    repo_path TEXT,
    settings TEXT DEFAULT '{}',
    sort_order INTEGER DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS agent_profiles (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    avatar TEXT,
    system_prompt TEXT NOT NULL,
    description TEXT,
    modes TEXT DEFAULT '[]',
    default_mode TEXT DEFAULT 'default',
    default_model TEXT,
    mcp_servers TEXT DEFAULT '[]',
    tool_permissions TEXT DEFAULT '{}',
    can_execute BOOLEAN DEFAULT FALSE,
    settings TEXT DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS agent_modes (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL REFERENCES agent_profiles(id),
    slug TEXT NOT NULL,
    name TEXT NOT NULL,
    prompt_addendum TEXT NOT NULL,
    tool_overrides TEXT DEFAULT '{}',
    settings TEXT DEFAULT '{}',
    UNIQUE(agent_id, slug)
);

CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    short_code TEXT NOT NULL UNIQUE,
    title TEXT,
    custom_name TEXT,
    workspace_id TEXT REFERENCES workspaces(id),
    project_id TEXT REFERENCES projects(id),
    context_type TEXT,
    context_id TEXT,
    provider TEXT,
    model TEXT,
    status TEXT DEFAULT 'active' CHECK(status IN ('active','paused','archived')),
    is_pinned BOOLEAN DEFAULT FALSE,
    sort_order INTEGER DEFAULT 0,
    message_count INTEGER DEFAULT 0,
    compaction_summary TEXT,
    compacted_at DATETIME,
    metadata TEXT DEFAULT '{}',
    last_activity DATETIME DEFAULT CURRENT_TIMESTAMP,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS session_agents (
    session_id TEXT NOT NULL REFERENCES sessions(id),
    agent_id TEXT NOT NULL REFERENCES agent_profiles(id),
    mode TEXT DEFAULT 'default',
    joined_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    is_primary BOOLEAN DEFAULT FALSE,
    PRIMARY KEY (session_id, agent_id)
);

CREATE TABLE IF NOT EXISTS messages (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id),
    agent_id TEXT REFERENCES agent_profiles(id),
    role TEXT NOT NULL CHECK(role IN ('user','assistant','system','tool')),
    content TEXT NOT NULL,
    envelope TEXT,
    metadata TEXT DEFAULT '{}',
    parent_id TEXT REFERENCES messages(id),
    is_compacted BOOLEAN DEFAULT FALSE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS bookmarks (
    id TEXT PRIMARY KEY,
    message_id TEXT NOT NULL REFERENCES messages(id),
    session_id TEXT NOT NULL REFERENCES sessions(id),
    note TEXT,
    tags TEXT DEFAULT '[]',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS artifacts (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id),
    message_id TEXT REFERENCES messages(id),
    name TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size_bytes INTEGER,
    storage_path TEXT NOT NULL,
    metadata TEXT DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS providers (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    provider_type TEXT NOT NULL,
    api_key TEXT,
    base_url TEXT,
    is_enabled BOOLEAN DEFAULT TRUE,
    settings TEXT DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS models (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL REFERENCES providers(id),
    model_id TEXT NOT NULL,
    display_name TEXT NOT NULL,
    context_window INTEGER,
    max_output INTEGER,
    supports_tools BOOLEAN DEFAULT FALSE,
    supports_vision BOOLEAN DEFAULT FALSE,
    pricing TEXT DEFAULT '{}',
    is_enabled BOOLEAN DEFAULT TRUE,
    sort_order INTEGER DEFAULT 0,
    UNIQUE(provider_id, model_id)
);

CREATE TABLE IF NOT EXISTS workflows (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    trigger TEXT NOT NULL,
    definition TEXT NOT NULL,
    workspace_id TEXT REFERENCES workspaces(id),
    is_enabled BOOLEAN DEFAULT TRUE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_sessions_workspace ON sessions(workspace_id, last_activity DESC);
CREATE INDEX IF NOT EXISTS idx_sessions_pinned ON sessions(is_pinned, sort_order);
CREATE INDEX IF NOT EXISTS idx_bookmarks_session ON bookmarks(session_id);
CREATE INDEX IF NOT EXISTS idx_artifacts_session ON artifacts(session_id);
CREATE INDEX IF NOT EXISTS idx_session_agents ON session_agents(session_id);
