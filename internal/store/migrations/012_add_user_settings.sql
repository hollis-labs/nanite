-- User-level settings (singleton row).
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
    settings TEXT NOT NULL DEFAULT '{}',
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Ensure exactly one row exists.
INSERT OR IGNORE INTO user_settings (id) VALUES (1);

-- Add default_provider to agent_profiles for agent-level provider preference.
ALTER TABLE agent_profiles ADD COLUMN default_provider TEXT NOT NULL DEFAULT '';
