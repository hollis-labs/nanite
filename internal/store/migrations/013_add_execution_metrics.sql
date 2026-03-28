-- Execution metrics: full snapshot of each LLM call for observability.
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
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_execution_metrics_session ON execution_metrics(session_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_execution_metrics_provider ON execution_metrics(provider, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_execution_metrics_utility ON execution_metrics(is_utility, created_at DESC);
