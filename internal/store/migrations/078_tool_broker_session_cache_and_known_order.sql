-- +goose Up
-- Tool broker ergonomics pass (2026-05-21).
--
-- 1. Result-cache pointers are session-scoped already, so durable agents need
--    a session-scale retention default rather than the original one-hour TTL.
--    Preserve explicit custom values and only migrate rows that still carry
--    the old default.
-- 2. Known-tool ordering should be an operator-authored per-agent sort hint,
--    not a runtime activation echo chamber. Add sort_order and backfill it
--    from agent_profiles.role_tools array order when available.

ALTER TABLE agent_known_tools ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0;

UPDATE agent_known_tools
SET sort_order = COALESCE((
    SELECT CAST(json_each.key AS INTEGER) + 1
    FROM agent_profiles,
         json_each(CASE WHEN json_valid(agent_profiles.role_tools) THEN agent_profiles.role_tools ELSE '[]' END)
    WHERE agent_profiles.id = agent_known_tools.agent_id
      AND json_each.value = agent_known_tools.tool_name
    LIMIT 1
), sort_order)
WHERE sort_order = 0;

UPDATE user_settings SET tool_result_cache_ttl_seconds = 31536000
    WHERE tool_result_cache_ttl_seconds IN (3600, 2592000);

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
