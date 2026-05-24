-- FU-7a: role_tools column on agent_profiles.
--
-- A JSON array of tool-name patterns the agent should be pre-seeded with
-- at create time. The boot-time hook in internal/api/agents.go reads this
-- column, parses the JSON array, and inserts one agent_known_tools row per
-- entry with pinned=1, reason='role_seed'. The seed is a best-effort
-- enrichment, NOT a contract the runtime enforces.
--
-- Empty array is the column default so existing rows and rows created by
-- callers that don't populate the field don't break the JSON-parse hook.
--
-- The migration runner has no schema_migrations table, so every migration
-- re-runs on every boot. ALTER TABLE ADD COLUMN is handled by the runner's
-- "duplicate column" tolerance (see internal/store/store.go), so the plain
-- ALTER without a guard is idempotent in practice. NOTE: the runner splits
-- statements on the semicolon character, including inside comments, so
-- this file uses none.

ALTER TABLE agent_profiles ADD COLUMN role_tools TEXT NOT NULL DEFAULT '[]'
