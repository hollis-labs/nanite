-- +goose Up
CREATE TABLE IF NOT EXISTS retired_agent_profiles (
    id TEXT PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT '',
    plugin_id TEXT NOT NULL DEFAULT '',
    class TEXT NOT NULL DEFAULT '',
    export_id TEXT NOT NULL,
    digest TEXT NOT NULL,
    actor TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    retired_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_retired_agent_profiles_plugin
    ON retired_agent_profiles(plugin_id);

-- +goose Down
DROP INDEX IF EXISTS idx_retired_agent_profiles_plugin;
DROP TABLE IF EXISTS retired_agent_profiles;
