-- +goose Up
-- Phase B — SpawnRequest.Provider override (CW-20260424-0004).
-- Adds a provider column to subagent_runs so the per-spawn provider
-- override is persisted and visible via Status / inspection.
-- Empty string means "used agent profile default" (backward-compatible).
-- Idempotent: runner swallows duplicate-column errors on re-run.
ALTER TABLE subagent_runs ADD COLUMN provider TEXT NOT NULL DEFAULT '';

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
