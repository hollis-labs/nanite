-- Phase 5 item 03 (TASKS/phase-5/03-wire-registers-agent-profiles.md):
-- wires `registers.agent_profiles[]` up to the role/agent composition model
-- (architecture/01-agent-construction.md), replacing the deferred-skip stub
-- in internal/plugin/registrations.go. A plugin's declared agent profile
-- constructs/upserts a real `roles` row (persona/system_prompt) plus a real
-- `agent_profiles` row (the composition record -- still `agent_profiles`
-- under the hood per decision log Section 6, not renamed to `agents`).
--
-- plugin_id tracks which plugin created a given roles/agent_profiles row so
-- plugin.Host.UnloadPlugin's "proper unload sweep across every registry a
-- plugin can touch" (architecture/09-plugin-system.md) can find and tear
-- down exactly what that plugin registered -- mirroring the existing
-- artifacts.source_plugin_id precedent (001_schema.sql) rather than
-- inventing a new ownership-tracking shape. Deliberately DB-authoritative
-- (a real column, read by internal/store/agents.go's ListAgentsByPluginID /
-- internal/store/roles.go's ListRolesByPluginID) instead of an in-memory
-- host-side map: an in-memory map would only be correct for "unload while
-- the plugin is loaded in this process," but internal/api/plugins.go's
-- handleUninstall also needs to clean up a plugin that was uninstalled
-- while already disabled (never loaded into the running host at all) --
-- see that file's runPluginUninstallCleanup/handleUninstall split.
--
-- No FK constraint (same choice artifacts.source_plugin_id made): plugin
-- identity isn't guaranteed to have a corresponding `plugins` (Phase 5 item
-- 02, migration 121) row at the moment a builtin's manifest registrations
-- run relative to when that table's row gets seeded, and a plugin can be
-- fully uninstalled (its `plugins` row deleted) while its DB-authoritative
-- agent/role rows are being torn down in the same operation -- an FK here
-- would fight that ordering instead of helping it. Both columns are plain,
-- nullable TEXT: NULL/empty means "not plugin-owned" (operator-created via
-- the GUI/API, or a pre-Phase-5-item-03 row), matching every other
-- ownership-tag column in this schema (agent_profiles.consumer_id, e.g.).

-- +goose Up
ALTER TABLE agent_profiles ADD COLUMN plugin_id TEXT;
ALTER TABLE roles ADD COLUMN plugin_id TEXT;

CREATE INDEX IF NOT EXISTS idx_agent_profiles_plugin_id ON agent_profiles(plugin_id);
CREATE INDEX IF NOT EXISTS idx_roles_plugin_id ON roles(plugin_id);

-- +goose Down
DROP INDEX IF EXISTS idx_roles_plugin_id;
DROP INDEX IF EXISTS idx_agent_profiles_plugin_id;
ALTER TABLE roles DROP COLUMN plugin_id;
ALTER TABLE agent_profiles DROP COLUMN plugin_id;
