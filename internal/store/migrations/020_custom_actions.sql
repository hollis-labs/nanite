-- Custom actions: user-defined automation bindings.
-- Actions can be triggered by keybindings, slash commands, or auto-triggers (events).

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
