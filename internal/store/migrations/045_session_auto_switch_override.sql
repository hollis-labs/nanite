-- +goose Up
-- F2 (CW-20260429-0002): per-session override for the auto-mode-switch
-- behavior. Nullable BOOLEAN — NULL means inherit the user-level
-- mode_auto_switch_pref (introduced in 041). A non-null value wins over
-- the user pref for that session.
--
-- "1" = force ON for this session (still does NOT bypass first-use prompt).
-- "0" = force OFF for this session (suppress all auto-switches).
-- NULL = inherit user pref.
--
-- IMPORTANT: no semicolons inside comments.
ALTER TABLE sessions
    ADD COLUMN auto_switch_override INTEGER DEFAULT NULL;

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
