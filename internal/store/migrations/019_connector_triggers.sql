-- Connector trigger rules: event → connector dispatch bindings.
-- When an event fires, matching enabled rules invoke the named connector
-- with a templated payload.

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
