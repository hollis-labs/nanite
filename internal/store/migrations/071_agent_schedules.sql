-- +goose Up
-- Per-agent scheduled directives — substrate for FU-27 composer pipeline.
--
-- Rows are authored by operator, agent (self-schedule), or system. The
-- composer (internal/scheduler or similar) calls GetDueSchedules per tick
-- to pull rows whose firing criteria match the current tick context, then
-- assembles their bodies into the per-tick procedure body.
--
-- schedule_kind semantics:
--   every_n_ticks  spec="5"          fires when tickN % spec == 0
--   on_tick        spec="42"         fires when tickN == spec then expires
--   cron           spec="0 9 * * *"  fires on the next tick after cron matches
--   one_shot       spec=""           fires once on the next matching tick
--   on_event       spec="mail_..."   fires once after the named event happens
--
-- The migration runner has no schema_migrations ledger and splits on the
-- semicolon character including inside comments, so this file avoids
-- semicolons in comment prose and uses IF NOT EXISTS for idempotency.
--
-- Table:
--   agent_schedules   directive rows (operator, agent, system authored)

CREATE TABLE IF NOT EXISTS agent_schedules (
    id              TEXT PRIMARY KEY,
    agent_id        TEXT NOT NULL REFERENCES agent_profiles(id),
    session_id      TEXT,
    name            TEXT NOT NULL,
    schedule_kind   TEXT NOT NULL CHECK (
        schedule_kind IN ('every_n_ticks','on_tick','cron','one_shot','on_event')
    ),
    schedule_spec   TEXT NOT NULL DEFAULT '',
    body            TEXT NOT NULL,
    priority        INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'active' CHECK (
        status IN ('active','paused','expired')
    ),
    expires_at      TEXT,
    fired_count     INTEGER NOT NULL DEFAULT 0,
    last_fired_at   TEXT,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    created_by      TEXT NOT NULL DEFAULT 'operator'
);

CREATE INDEX IF NOT EXISTS idx_agent_schedules_lookup
    ON agent_schedules(agent_id, session_id, status);

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
