-- +goose Up
-- B1 (CW-20260428-0009): session-level mode pointer.
-- Nullable, null = fall back to agent's assigned mode (back-compat).
-- IMPORTANT: no semicolons inside comments.

ALTER TABLE sessions
    ADD COLUMN current_mode_id TEXT REFERENCES modes(id);

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
