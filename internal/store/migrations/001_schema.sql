-- Nanite schema (squashed from 27 migrations, 2026-04-04).
-- DDL only. All seed data lives in seed.go.

-- ─── Core ────────────────────────────────────────────────

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

-- ─── Agents ──────────────────────────────────────────────

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
    default_provider TEXT NOT NULL DEFAULT '',
    mcp_servers TEXT DEFAULT '[]',
    tool_permissions TEXT DEFAULT '{}',
    can_execute BOOLEAN DEFAULT FALSE,
    settings TEXT DEFAULT '{}',
    -- v2 fields
    agent_hash TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    tools TEXT NOT NULL DEFAULT '[]',
    directories TEXT NOT NULL DEFAULT '[]',
    constraints TEXT NOT NULL DEFAULT '{}',
    tags TEXT NOT NULL DEFAULT '[]',
    status TEXT NOT NULL DEFAULT 'active',
    source TEXT NOT NULL DEFAULT 'system',
    source_ref TEXT NOT NULL DEFAULT '',
    icon TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS agent_modes (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,  -- no FK: agents may be file-based (not in agent_profiles)
    slug TEXT NOT NULL,
    name TEXT NOT NULL,
    prompt_addendum TEXT NOT NULL,
    tool_overrides TEXT DEFAULT '{}',
    settings TEXT DEFAULT '{}',
    UNIQUE(agent_id, slug)
);

CREATE TABLE IF NOT EXISTS agent_projects (
    agent_id TEXT NOT NULL,  -- no FK: agents may be file-based
    project_id TEXT NOT NULL REFERENCES projects(id),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (agent_id, project_id)
);

CREATE INDEX IF NOT EXISTS idx_agent_projects_project ON agent_projects(project_id);

-- ─── Sessions ────────────────────────────────────────────

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
    tags TEXT DEFAULT '[]',
    metadata TEXT DEFAULT '{}',
    last_activity DATETIME DEFAULT CURRENT_TIMESTAMP,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS session_agents (
    session_id TEXT NOT NULL REFERENCES sessions(id),
    agent_id TEXT NOT NULL,  -- no FK: agents may be file-based
    mode TEXT DEFAULT 'default',
    joined_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    is_primary BOOLEAN DEFAULT FALSE,
    PRIMARY KEY (session_id, agent_id)
);

CREATE INDEX IF NOT EXISTS idx_sessions_workspace ON sessions(workspace_id, last_activity DESC);
CREATE INDEX IF NOT EXISTS idx_sessions_pinned ON sessions(is_pinned, sort_order);
CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions(status);
CREATE INDEX IF NOT EXISTS idx_session_agents ON session_agents(session_id);

-- ─── Messages ────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS messages (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id),
    agent_id TEXT,  -- no FK: agents may be file-based
    role TEXT NOT NULL CHECK(role IN ('user','assistant','system','tool')),
    content TEXT NOT NULL,
    envelope TEXT,
    metadata TEXT DEFAULT '{}',
    parent_id TEXT REFERENCES messages(id),
    is_compacted BOOLEAN DEFAULT FALSE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_messages_content ON messages(session_id, created_at);

-- ─── Bookmarks & Artifacts ──────────────────────────────

CREATE TABLE IF NOT EXISTS bookmarks (
    id TEXT PRIMARY KEY,
    message_id TEXT NOT NULL REFERENCES messages(id),
    session_id TEXT NOT NULL REFERENCES sessions(id),
    note TEXT,
    tags TEXT DEFAULT '[]',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_bookmarks_session ON bookmarks(session_id);

CREATE TABLE IF NOT EXISTS artifacts (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id),
    message_id TEXT REFERENCES messages(id),
    name TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size_bytes INTEGER,
    storage_path TEXT NOT NULL,
    metadata TEXT DEFAULT '{}',
    origin TEXT NOT NULL DEFAULT 'uploaded',
    source_tool_call_id TEXT,
    source_agent_id TEXT,
    source_plugin_id TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_artifacts_session ON artifacts(session_id);

-- ─── Providers & Models ─────────────────────────────────

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

-- ─── Skills ─────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS skills (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    tool_bindings TEXT NOT NULL DEFAULT '[]',
    input_schema TEXT NOT NULL DEFAULT '{}',
    is_builtin INTEGER NOT NULL DEFAULT 0,
    settings TEXT NOT NULL DEFAULT '{}',
    icon TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_skills (
    agent_id TEXT NOT NULL,  -- no FK: agents may be file-based
    skill_id TEXT NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    config TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY (agent_id, skill_id)
);

-- ─── Prompt Templates ───────────────────────────────────

CREATE TABLE IF NOT EXISTS prompt_templates (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    scope TEXT NOT NULL CHECK(scope IN ('system','mode','skill','context')),
    template TEXT NOT NULL,
    variables TEXT NOT NULL DEFAULT '[]',
    priority INTEGER NOT NULL DEFAULT 0,
    is_builtin INTEGER NOT NULL DEFAULT 0,
    icon TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_prompt_templates (
    agent_id TEXT NOT NULL,  -- no FK: agents may be file-based
    template_id TEXT NOT NULL REFERENCES prompt_templates(id) ON DELETE CASCADE,
    PRIMARY KEY (agent_id, template_id)
);

-- ─── Modes ──────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS modes (
    id TEXT PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    prompt_addendum TEXT NOT NULL DEFAULT '',
    tool_overrides TEXT NOT NULL DEFAULT '{}',
    settings TEXT NOT NULL DEFAULT '{}',
    is_builtin INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_mode_assignments (
    agent_id TEXT NOT NULL,  -- no FK: agents may be file-based
    mode_id TEXT NOT NULL REFERENCES modes(id) ON DELETE CASCADE,
    PRIMARY KEY (agent_id, mode_id)
);

-- ─── Templates ──────────────────────────────────────────

CREATE TABLE IF NOT EXISTS templates (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    template TEXT NOT NULL,
    is_builtin INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- ─── MCP Servers ────────────────────────────────────────

CREATE TABLE IF NOT EXISTS mcp_servers (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    transport_type TEXT NOT NULL DEFAULT 'stdio',
    command TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    args TEXT NOT NULL DEFAULT '[]',
    env TEXT NOT NULL DEFAULT '[]',
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- ─── A2A Messaging ──────────────────────────────────────

CREATE TABLE IF NOT EXISTS a2a_messages (
    id TEXT PRIMARY KEY,
    from_agent TEXT NOT NULL,
    to_agent TEXT NOT NULL,
    thread_id TEXT,
    reply_to TEXT REFERENCES a2a_messages(id),
    type TEXT NOT NULL DEFAULT 'message' CHECK(type IN ('message','help_request','directive','status_update','handoff')),
    subject TEXT,
    body TEXT NOT NULL,
    metadata TEXT DEFAULT '{}',
    priority INTEGER DEFAULT 2,
    status TEXT DEFAULT 'unread' CHECK(status IN ('unread','read','acknowledged','resolved')),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    read_at DATETIME,
    resolved_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_a2a_inbox ON a2a_messages(to_agent, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_a2a_thread ON a2a_messages(thread_id, created_at);

-- ─── User Settings ──────────────────────────────────────

CREATE TABLE IF NOT EXISTS user_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    provider_fallback_chain TEXT NOT NULL DEFAULT '[]',
    default_provider TEXT NOT NULL DEFAULT '',
    default_model TEXT NOT NULL DEFAULT '',
    default_adapter TEXT NOT NULL DEFAULT '',
    default_agent TEXT NOT NULL DEFAULT '',
    utility_provider TEXT NOT NULL DEFAULT '',
    utility_model TEXT NOT NULL DEFAULT '',
    tool_call_display_mode TEXT NOT NULL DEFAULT 'minimal',
    developer_mode INTEGER NOT NULL DEFAULT 0,
    recover_mode INTEGER NOT NULL DEFAULT 0,
    tool_stream_behavior TEXT NOT NULL DEFAULT 'streaming',
    tool_drawer_retention INTEGER NOT NULL DEFAULT 15,
    tool_load_preferences TEXT NOT NULL DEFAULT '{}',
    settings TEXT NOT NULL DEFAULT '{}',
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- ─── Observability ──────────────────────────────────────

CREATE TABLE IF NOT EXISTS execution_metrics (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    message_id TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT '',
    adapter TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    agent_id TEXT NOT NULL DEFAULT '',
    agent_slug TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    context_messages INTEGER NOT NULL DEFAULT 0,
    context_tokens INTEGER NOT NULL DEFAULT 0,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    estimated_cost_usd REAL NOT NULL DEFAULT 0.0,
    tool_iterations INTEGER NOT NULL DEFAULT 0,
    tool_calls INTEGER NOT NULL DEFAULT 0,
    is_utility BOOLEAN NOT NULL DEFAULT FALSE,
    stop_reason TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    debug_snapshots TEXT DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_execution_metrics_session ON execution_metrics(session_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_execution_metrics_provider ON execution_metrics(provider, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_execution_metrics_utility ON execution_metrics(is_utility, created_at DESC);

CREATE TABLE IF NOT EXISTS token_usage (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES sessions(id),
    message_id TEXT NOT NULL,
    model TEXT NOT NULL,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    tool_input_tokens INTEGER NOT NULL DEFAULT 0,
    estimated_cost_usd REAL NOT NULL DEFAULT 0.0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_token_usage_session ON token_usage(session_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_token_usage_model ON token_usage(model);

CREATE TABLE IF NOT EXISTS broker_decisions (
    id INTEGER PRIMARY KEY,
    session_id TEXT,
    intent TEXT,
    layer_reached TEXT,
    selected_tools TEXT,
    signals TEXT,
    created_at TEXT DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_broker_decisions_session ON broker_decisions(session_id);

-- ─── Plugins ────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS plugin_settings (
    plugin_id TEXT PRIMARY KEY,
    settings TEXT NOT NULL DEFAULT '{}',
    schema TEXT NOT NULL DEFAULT '{}',
    icon TEXT,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS catalog_sources (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    type TEXT NOT NULL DEFAULT 'custom',
    enabled INTEGER NOT NULL DEFAULT 1,
    priority INTEGER NOT NULL DEFAULT 0,
    public_key TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_catalog_sources_url ON catalog_sources(url);

-- ─── Connector Triggers ─────────────────────────────────

CREATE TABLE IF NOT EXISTS trigger_rules (
    id TEXT PRIMARY KEY,
    plugin_id TEXT NOT NULL DEFAULT '',
    event_type TEXT NOT NULL,
    connector_name TEXT NOT NULL,
    payload_template TEXT NOT NULL DEFAULT '{}',
    filter_expr TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    description TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_trigger_rules_event_type ON trigger_rules(event_type);
CREATE INDEX IF NOT EXISTS idx_trigger_rules_plugin_id ON trigger_rules(plugin_id);

-- ─── Custom Actions ─────────────────────────────────────

CREATE TABLE IF NOT EXISTS custom_actions (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    keybinding TEXT NOT NULL DEFAULT '',
    command TEXT NOT NULL DEFAULT '',
    slash_command TEXT NOT NULL DEFAULT '',
    auto_triggers TEXT NOT NULL DEFAULT '[]',
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_custom_actions_name ON custom_actions(name);

-- ─── Workflows ──────────────────────────────────────────

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

-- ─── Event Log ──────────────────────────────────────────

CREATE TABLE IF NOT EXISTS event_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT,
    event_type TEXT NOT NULL,
    category TEXT NOT NULL DEFAULT 'general',
    detail TEXT,
    metadata TEXT DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_event_log_type ON event_log(event_type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_event_log_category ON event_log(category, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_event_log_session ON event_log(session_id, created_at DESC);

-- ─── Tasks ──────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS tasks (
    id TEXT PRIMARY KEY,
    parent_id TEXT,
    session_id TEXT NOT NULL,
    worker_session_id TEXT,
    title TEXT NOT NULL,
    description TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    assignee_agent_id TEXT,
    result TEXT,
    error TEXT,
    tokens_used INTEGER DEFAULT 0,
    metadata TEXT DEFAULT '{}',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    completed_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_tasks_session ON tasks(session_id);
CREATE INDEX IF NOT EXISTS idx_tasks_parent ON tasks(parent_id);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
