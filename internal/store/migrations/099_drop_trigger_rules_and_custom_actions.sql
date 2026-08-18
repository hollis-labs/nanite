-- +goose Up
-- Drop trigger_rules and custom_actions — dead surface, operator-confirmed
-- direct cut, TASKS/phase-0/18b Part B.
--
-- Architecture doc docs/engineering/architecture/09-plugin-system.md
-- ("Cut"): "trigger_rules (an early version of what became reflexes) and
-- custom_actions (meant to be slash-command-triggered UI actions) — both
-- zero-caller, zero-row. Whatever real need either was reaching for is
-- served by reflexes and the existing, real plugin commands[] registration
-- going forward."
--
-- CORRECTION recorded per EXECUTION-PROCESS.md worker step 7: "zero-caller"
-- did not hold literally at implementation time. Both tables backed a real,
-- reachable HTTP surface (internal/api/triggers.go's 5 CRUD handlers,
-- internal/api/actions.go's 6 handlers including a real POST
-- /api/actions/id/execute), a real in-process trigger-dispatch engine
-- (internal/plugin/triggers.go's TriggerDispatcher, wired into
-- internal/plugin/host.go's EmitEvent), and a real frontend consumer for
-- custom_actions specifically (ui/src/components/settings/ActionsPanel.tsx,
-- reachable via SettingsPage.tsx though its nav entry was commented out
-- pending a scope review, plus a genuinely live global keybinding-dispatch
-- path in ui/src/hooks/useKeyboardShortcuts.ts's useActionKeybindings,
-- mounted app-wide via AppShell.tsx, independent of that hidden nav entry).
-- No frontend surface existed for trigger_rules specifically. Per the
-- operator's final decision recorded in this task's own brief, this does
-- not change the outcome: the whole surface (backend CRUD, dispatch
-- engine, and frontend consumers) was removed alongside this migration —
-- see TASKS/phase-0/18b-cut-dead-messaging-and-plugin-tables.md's Work Log
-- for the full accounting.
--
-- The Down recreates both tables exactly as migration 001 defined them.

DROP TABLE IF EXISTS trigger_rules;

DROP TABLE IF EXISTS custom_actions;

-- +goose Down

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
