-- +goose Up
-- B3 (CW-20260428-0011): user-level preference for auto-applying classifier
-- mode suggestions. Empty string = unset → triggers first-use prompt.
-- Allowed values: "" (unset), "always", "ask", "never".
-- IMPORTANT: no semicolons inside comments.
ALTER TABLE user_settings
    ADD COLUMN mode_auto_switch_pref TEXT NOT NULL DEFAULT '';

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
