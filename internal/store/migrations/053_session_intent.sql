-- +goose Up
-- Glass-3 (CW-20260502-0011, SP-20260502-0001) — session intent for auto-handoff classification.
-- NULL means unclassified. Classification fires in Glass-4 at session start.
-- Enum values long-running, per-turn, ephemeral are enforced by the CHECK below.
-- NOTE no semicolons inside comments (splitSQL naive-split limitation).

ALTER TABLE sessions ADD COLUMN intent TEXT NULL CHECK (intent IS NULL OR intent IN ('long-running', 'per-turn', 'ephemeral'));

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
