-- +goose Up
-- S7 T5: Agent profile registry extension.
--
-- Adds minimal registry columns cherry-picked from the Nexus registry
-- concept. Broker / capability matching is post-MVP — these columns
-- exist so the future broker has something to read, and so T6's
-- auto-register-on-first-send can stamp kind='external' or 'cli' on
-- auto-discovered callers.
--
-- Columns:
--   kind              — internal (default, existing DB/file agents),
--                       external (discovered via message_send),
--                       cli (CLI callers with deterministic IDs)
--   capabilities_json — JSON array of capability slugs (empty now;
--                       broker will use later)
--   limits_json       — JSON blob of per-agent limits (rate, budget,
--                       etc). Free-form for now.
--   model_strategy    — free-form text. The broker will later parse
--                       this to decide model routing for the agent.

ALTER TABLE agent_profiles ADD COLUMN kind TEXT NOT NULL DEFAULT 'internal'
  CHECK(kind IN ('internal','external','cli'));

ALTER TABLE agent_profiles ADD COLUMN capabilities_json TEXT NOT NULL DEFAULT '[]';

ALTER TABLE agent_profiles ADD COLUMN limits_json TEXT NOT NULL DEFAULT '{}';

ALTER TABLE agent_profiles ADD COLUMN model_strategy TEXT NOT NULL DEFAULT '';

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
