-- +goose Up
-- E2 (CW-20260428-0017): mode binding for skills.
-- Adds a JSON array column "mode_ids" to skills storing the mode IDs the
-- skill is bound to. Empty / "[]" / NULL means available in every mode
-- (back-compat default).
-- Slugs in the file frontmatter are translated to mode IDs at ingest time.
-- Migration runner swallows duplicate-column errors for ADD COLUMN.
-- IMPORTANT: no semicolons inside comments.

ALTER TABLE skills
    ADD COLUMN mode_ids TEXT NOT NULL DEFAULT '[]';

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
